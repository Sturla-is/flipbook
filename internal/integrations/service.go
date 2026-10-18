package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/storage"
	"github.com/metavert-io/flipbook/internal/worker"
)

const (
	oauthStateTTL          = 10 * time.Minute
	OAuthBindingCookieName = "flipbook_integration_oauth"
	maxOAuthStateEntries   = 10000
)

type oauthState struct {
	Provider    string
	WorkspaceID string
	UserID      string
	Verifier    string
	Binding     string
	ExpiresAt   time.Time
}

type Service struct {
	store  Store
	assets AssetStore
	queue  ConversionQueue
	opts   Options
	cipher *TokenCipher

	now    func() time.Time
	random io.Reader

	stateMu sync.Mutex
	states  map[string]oauthState
	locks   sync.Map
}

var _ Store = (*database.DB)(nil)
var _ AssetStore = (*storage.Storage)(nil)
var _ ConversionQueue = (*worker.Worker)(nil)

func NewService(store Store, assets AssetStore, queue ConversionQueue, options Options) (*Service, error) {
	if store == nil || assets == nil || queue == nil {
		return nil, errors.New("integration store, asset store, and conversion queue are required")
	}
	if options.Providers == nil {
		options.Providers = DefaultProviderConfigs()
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if options.DownloadClient == nil {
		options.DownloadClient = &http.Client{
			Timeout: 2 * time.Minute,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 || !secureProviderURL(request.URL.String()) {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
	}
	if options.MaxDownloadBytes == 0 {
		options.MaxDownloadBytes = 200 << 20
	}
	if options.MaxDownloadBytes < 1 {
		return nil, errors.New("integration download limit must be positive")
	}
	if options.ExportPollInterval == 0 {
		options.ExportPollInterval = time.Second
	}
	if options.ExportPollInterval < 0 {
		return nil, errors.New("integration export poll interval cannot be negative")
	}
	if options.ExportPollAttempts == 0 {
		options.ExportPollAttempts = 30
	}
	if options.ExportPollAttempts < 1 || options.ExportPollAttempts > 1000 {
		return nil, errors.New("integration export poll attempts must be between 1 and 1000")
	}
	if options.WebhookMaxAttempts == 0 {
		options.WebhookMaxAttempts = 8
	}
	if options.WebhookMaxAttempts < 1 || options.WebhookMaxAttempts > 100 {
		return nil, errors.New("integration webhook attempts must be between 1 and 100")
	}
	var cipher *TokenCipher
	var err error
	if options.EncryptionKey != "" {
		cipher, err = newTokenCipher(options.EncryptionKey, options.Random)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		store: store, assets: assets, queue: queue, opts: options, cipher: cipher,
		now: options.Now, random: options.Random, states: make(map[string]oauthState),
	}, nil
}

func (s *Service) nowUTC() time.Time { return s.now().UTC() }

func (s *Service) providerConfig(name string) (ProviderConfig, error) {
	config, ok := s.opts.Providers[name]
	if !ok || (name != ProviderCanva && name != ProviderAdobeExpress) {
		return ProviderConfig{}, integrationError(ErrNotFound, name, "configuration", "The requested integration provider is not supported.")
	}
	return config, nil
}

func (s *Service) ProviderCatalog() []ProviderInfo {
	catalog := make([]ProviderInfo, 0, 2)
	for _, name := range []string{ProviderCanva, ProviderAdobeExpress} {
		config, _ := s.providerConfig(name)
		info := ProviderInfo{
			Name: name, DisplayName: providerDisplayName(name), Configured: config.Configured() && s.cipher != nil,
			SupportsStatistics: strings.TrimSpace(config.StatisticsPath) != "",
		}
		switch name {
		case ProviderCanva:
			info.ApprovalNote = "Requires a Canva Connect app with design metadata and content export permissions."
		case ProviderAdobeExpress:
			info.ApprovalNote = "Requires Adobe Express API access and an approved Adobe Developer Console credential. Document metadata is returned as provider statistics; engagement analytics are not assumed."
		}
		switch {
		case !config.Enabled:
			info.ConfigurationMessage = "Provider OAuth is disabled in configuration."
		case !config.Configured():
			info.ConfigurationMessage = "Configure the OAuth client ID, client secret, redirect URL, authorization endpoints, and API base URL."
		case s.cipher == nil:
			info.ConfigurationMessage = "Set security.token_encryption_key to at least 32 bytes before connecting."
		}
		catalog = append(catalog, info)
	}
	return catalog
}

func providerDisplayName(name string) string {
	switch name {
	case ProviderCanva:
		return "Canva"
	case ProviderAdobeExpress:
		return "Adobe Express"
	default:
		return name
	}
}

func publicConnection(connection *models.IntegrationConnection) *models.IntegrationConnection {
	if connection == nil {
		return nil
	}
	clone := *connection
	clone.AccessTokenEncrypted = ""
	clone.RefreshTokenEncrypted = ""
	return &clone
}

func (s *Service) Connections(ctx context.Context, workspaceID, userID string) ([]*models.IntegrationConnection, error) {
	connections, err := s.store.ListIntegrationConnections(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	for index, connection := range connections {
		connections[index] = publicConnection(connection)
	}
	return connections, nil
}

func (s *Service) findConnection(ctx context.Context, workspaceID, userID, provider string) (*models.IntegrationConnection, error) {
	connections, err := s.store.ListIntegrationConnections(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	var selected *models.IntegrationConnection
	for _, candidate := range connections {
		if candidate.Provider != provider {
			continue
		}
		if selected == nil || candidate.UpdatedAt.After(selected.UpdatedAt) ||
			(candidate.UpdatedAt.Equal(selected.UpdatedAt) && candidate.CreatedAt.After(selected.CreatedAt)) {
			selected = candidate
		}
	}
	if selected == nil {
		return nil, integrationError(ErrNotConnected, provider, "connection", "Connect "+providerDisplayName(provider)+" before using this integration.")
	}
	return selected, nil
}

func (s *Service) connectedConnection(ctx context.Context, workspaceID, userID, provider string) (*models.IntegrationConnection, error) {
	connection, err := s.findConnection(ctx, workspaceID, userID, provider)
	if err != nil {
		return nil, err
	}
	if connection.Status == models.IntegrationDisconnected || connection.AccessTokenEncrypted == "" {
		return nil, integrationError(ErrNotConnected, provider, "connection", "The "+providerDisplayName(provider)+" connection is disconnected or has no credential. Reconnect it to continue.")
	}
	return connection, nil
}

func randomIntegrationToken(random io.Reader) (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (s *Service) BeginOAuth(_ context.Context, request OAuthRequest) (*OAuthSession, error) {
	config, err := s.providerConfig(request.Provider)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.UserID) == "" {
		return nil, integrationError(ErrInvalidInput, request.Provider, "authorize", "A workspace and user are required to connect a provider.")
	}
	if !config.Configured() || s.cipher == nil {
		return nil, integrationError(ErrNotConfigured, request.Provider, "authorize", providerDisplayName(request.Provider)+" OAuth and token encryption must be configured before connecting.")
	}
	if err := validateProviderEndpoints(config); err != nil {
		return nil, err
	}
	state, err := randomIntegrationToken(s.random)
	if err != nil {
		return nil, err
	}
	verifier, err := randomIntegrationToken(s.random)
	if err != nil {
		return nil, err
	}
	binding, err := randomIntegrationToken(s.random)
	if err != nil {
		return nil, err
	}
	now := s.nowUTC()
	expires := now.Add(oauthStateTTL)
	s.stateMu.Lock()
	for key, pending := range s.states {
		if !now.Before(pending.ExpiresAt) {
			delete(s.states, key)
		}
	}
	if len(s.states) >= maxOAuthStateEntries {
		s.stateMu.Unlock()
		return nil, integrationError(ErrConflict, request.Provider, "authorize", "Too many authorization sessions are active. Try again shortly.")
	}
	s.states[state] = oauthState{
		Provider: request.Provider, WorkspaceID: request.WorkspaceID, UserID: request.UserID,
		Verifier: verifier, Binding: binding, ExpiresAt: expires,
	}
	s.stateMu.Unlock()
	challenge := sha256.Sum256([]byte(verifier))
	authorizationURL, err := url.Parse(config.AuthorizationURL)
	if err != nil {
		return nil, err
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", config.ClientID)
	query.Set("redirect_uri", config.RedirectURL)
	query.Set("state", state)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	if len(config.Scopes) > 0 {
		query.Set("scope", strings.Join(config.Scopes, " "))
	}
	authorizationURL.RawQuery = query.Encode()
	return &OAuthSession{AuthorizationURL: authorizationURL.String(), Binding: binding, ExpiresAt: expires}, nil
}

func (s *Service) consumeOAuthState(value string) (oauthState, bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	pending, ok := s.states[value]
	delete(s.states, value)
	if !ok || !s.nowUTC().Before(pending.ExpiresAt) {
		return oauthState{}, false
	}
	return pending, true
}

func (s *Service) CompleteOAuth(ctx context.Context, callback OAuthCallback) (*models.IntegrationConnection, error) {
	pending, ok := s.consumeOAuthState(callback.State)
	if !ok || callback.Binding == "" || !hmac.Equal([]byte(callback.Binding), []byte(pending.Binding)) ||
		callback.Provider != pending.Provider || callback.WorkspaceID != pending.WorkspaceID || callback.UserID != pending.UserID {
		return nil, integrationError(ErrInvalidState, callback.Provider, "callback", "The provider authorization session is invalid or expired. Start the connection again.")
	}
	if callback.Error != "" {
		return nil, integrationError(ErrPermission, callback.Provider, "callback", providerDisplayName(callback.Provider)+" authorization was denied or cancelled.")
	}
	if strings.TrimSpace(callback.Code) == "" {
		return nil, integrationError(ErrInvalidInput, callback.Provider, "callback", "The provider did not return an authorization code.")
	}
	config, err := s.providerConfig(callback.Provider)
	if err != nil {
		return nil, err
	}
	client := s.newProviderClient(callback.Provider, config)
	token, err := client.exchangeCode(ctx, callback.Code, pending.Verifier)
	if err != nil {
		return nil, err
	}
	connection, findErr := s.findConnection(ctx, callback.WorkspaceID, callback.UserID, callback.Provider)
	if findErr != nil && !errors.Is(findErr, ErrNotConnected) {
		return nil, findErr
	}
	if connection == nil {
		connection = &models.IntegrationConnection{
			ID: uuid.NewString(), WorkspaceID: callback.WorkspaceID, UserID: callback.UserID, Provider: callback.Provider,
		}
	}
	now := s.nowUTC()
	accessCiphertext, err := s.cipher.Seal(connectionTokenContext(connection.ID, "access"), token.AccessToken)
	if err != nil {
		return nil, err
	}
	refreshCiphertext := ""
	if token.RefreshToken != "" {
		refreshCiphertext, err = s.cipher.Seal(connectionTokenContext(connection.ID, "refresh"), token.RefreshToken)
		if err != nil {
			return nil, err
		}
	}
	connection.AccessTokenEncrypted = accessCiphertext
	connection.RefreshTokenEncrypted = refreshCiphertext
	connection.TokenExpiresAt = token.ExpiresAt(now)
	connection.Status = models.IntegrationConnected
	connection.Scopes = token.ScopeList(config.Scopes)
	connection.LastVerifiedAt = &now
	connection.LastError = ""
	connection.DisconnectedAt = nil
	if connection.DisplayName == "" {
		connection.DisplayName = providerDisplayName(callback.Provider)
	}
	if connection.Metadata == nil {
		connection.Metadata = make(map[string]string)
	}
	if token.TokenType != "" {
		connection.Metadata["token_type"] = token.TokenType
	}
	if token.ExternalAccountID != "" {
		connection.ExternalAccountID = token.ExternalAccountID
	}
	if connection.CreatedAt.IsZero() {
		if err := s.store.CreateIntegrationConnection(ctx, connection); err != nil {
			return nil, err
		}
	} else if err := s.store.UpdateIntegrationConnection(ctx, connection); err != nil {
		return nil, err
	}
	return publicConnection(connection), nil
}

func (s *Service) VerifyConnection(ctx context.Context, workspaceID, userID, provider string) (*models.IntegrationConnection, error) {
	config, err := s.providerConfig(provider)
	if err != nil {
		return nil, err
	}
	if !config.Configured() || s.cipher == nil {
		return nil, integrationError(ErrNotConfigured, provider, "verify connection", providerDisplayName(provider)+" OAuth, API endpoints, and token encryption must be configured before verification.")
	}
	if err := validateProviderEndpoints(config); err != nil {
		return nil, err
	}
	connection, err := s.connectedConnection(ctx, workspaceID, userID, provider)
	if err != nil {
		return nil, err
	}
	client := s.newProviderClient(provider, config)
	if err := client.verify(ctx, connection); err != nil {
		s.recordConnectionError(ctx, connection, err)
		return nil, err
	}
	if err := s.recordConnectionSuccess(ctx, connection); err != nil {
		return nil, err
	}
	return publicConnection(connection), nil
}

func (s *Service) recordConnectionError(ctx context.Context, connection *models.IntegrationConnection, providerErr error) {
	if connection == nil || providerErr == nil {
		return
	}
	connection.Status = models.IntegrationError
	connection.LastError = PublicMessage(providerErr)
	_ = s.store.UpdateIntegrationConnection(ctx, connection)
}

func (s *Service) recordConnectionSuccess(ctx context.Context, connection *models.IntegrationConnection) error {
	now := s.nowUTC()
	connection.Status = models.IntegrationConnected
	connection.LastVerifiedAt = &now
	connection.LastError = ""
	return s.store.UpdateIntegrationConnection(ctx, connection)
}

func (s *Service) Disconnect(ctx context.Context, workspaceID, userID, provider string) (*DisconnectResult, error) {
	config, err := s.providerConfig(provider)
	if err != nil {
		return nil, err
	}
	connection, err := s.findConnection(ctx, workspaceID, userID, provider)
	if err != nil {
		return nil, err
	}
	accessToken := ""
	var warning string
	if config.RevocationURL != "" && connection.AccessTokenEncrypted != "" && s.cipher != nil {
		accessToken, err = s.cipher.Open(connectionTokenContext(connection.ID, "access"), connection.AccessTokenEncrypted)
		if err != nil {
			warning = "The local connection was removed, but the provider token could not be revoked: " + PublicMessage(err)
			accessToken = ""
		}
	}
	if err := s.store.DisconnectIntegrationConnection(ctx, workspaceID, connection.ID); err != nil {
		return nil, err
	}
	result := &DisconnectResult{Disconnected: true, Warning: warning}
	if accessToken != "" {
		client := s.newProviderClient(provider, config)
		if err := client.revoke(ctx, accessToken); err != nil {
			result.Warning = "The local connection was removed, but provider revocation failed: " + PublicMessage(err)
		} else {
			result.ProviderRevoked = true
		}
	}
	return result, nil
}

func (s *Service) connectionLock(id string) *sync.Mutex {
	candidate, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return candidate.(*sync.Mutex)
}

func (s *Service) accessToken(ctx context.Context, connection *models.IntegrationConnection) (string, error) {
	lock := s.connectionLock(connection.ID)
	lock.Lock()
	defer lock.Unlock()
	needsRefresh := connection.TokenExpiresAt != nil && !s.nowUTC().Add(time.Minute).Before(*connection.TokenExpiresAt)
	if needsRefresh {
		if connection.RefreshTokenEncrypted == "" {
			return "", integrationError(ErrNotConnected, connection.Provider, "token", "The provider access token expired and no refresh token is available. Reconnect the provider.")
		}
		refreshToken, err := s.cipher.Open(connectionTokenContext(connection.ID, "refresh"), connection.RefreshTokenEncrypted)
		if err != nil {
			return "", err
		}
		config, err := s.providerConfig(connection.Provider)
		if err != nil {
			return "", err
		}
		client := s.newProviderClient(connection.Provider, config)
		token, err := client.refreshToken(ctx, refreshToken)
		if err != nil {
			return "", err
		}
		accessCiphertext, err := s.cipher.Seal(connectionTokenContext(connection.ID, "access"), token.AccessToken)
		if err != nil {
			return "", err
		}
		connection.AccessTokenEncrypted = accessCiphertext
		connection.TokenExpiresAt = token.ExpiresAt(s.nowUTC())
		if token.RefreshToken != "" && token.RefreshToken != refreshToken {
			connection.RefreshTokenEncrypted, err = s.cipher.Seal(connectionTokenContext(connection.ID, "refresh"), token.RefreshToken)
			if err != nil {
				return "", err
			}
		}
		if err := s.store.UpdateIntegrationConnection(ctx, connection); err != nil {
			return "", err
		}
	}
	token, err := s.cipher.Open(connectionTokenContext(connection.ID, "access"), connection.AccessTokenEncrypted)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", integrationError(ErrNotConnected, connection.Provider, "token", "No provider access token is stored. Reconnect the provider.")
	}
	return token, nil
}

func (s *Service) SecureCookies() bool {
	return s.opts.SecureCookies
}
