package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/worker"
)

func TestTokenCipherRoundTripAndContextBinding(t *testing.T) {
	cipher, err := NewTokenCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Seal("connection/one/access", "plain-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ciphertext, tokenCiphertextVersion) || strings.Contains(ciphertext, "plain-access-token") {
		t.Fatalf("unexpected ciphertext: %q", ciphertext)
	}
	plaintext, err := cipher.Open("connection/one/access", ciphertext)
	if err != nil || plaintext != "plain-access-token" {
		t.Fatalf("round trip failed: %q, %v", plaintext, err)
	}
	if _, err := cipher.Open("connection/two/access", ciphertext); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("wrong encryption context succeeded: %v", err)
	}
	tampered := ciphertext[:len(ciphertext)-2] + "AA"
	if _, err := cipher.Open("connection/one/access", tampered); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("tampered ciphertext succeeded: %v", err)
	}
	if _, err := NewTokenCipher("too-short"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("short encryption key was accepted: %v", err)
	}
}

func TestOAuthStatePKCEAndEncryptedTokenStorage(t *testing.T) {
	store := newFakeStore()
	queue := &fakeQueue{}
	var server *httptest.Server
	expectedVerifier := ""
	tokenCalls := 0
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			http.NotFound(writer, request)
			return
		}
		tokenCalls++
		clientID, secret, ok := request.BasicAuth()
		if !ok || clientID != "client-id" || secret != "client-secret" {
			t.Errorf("missing OAuth client authentication")
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("code") != "authorization-code" || request.Form.Get("code_verifier") != expectedVerifier {
			t.Errorf("invalid authorization code exchange: %v", request.Form)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "new-access-token", "refresh_token": "new-refresh-token",
			"token_type": "Bearer", "expires_in": 3600, "scope": "design:content:read design:meta:read", "user_id": "account-1",
		})
	}))
	defer server.Close()
	service := testService(t, store, queue, ProviderCanva, server.URL)
	session, err := service.BeginOAuth(context.Background(), OAuthRequest{Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	if state == "" || session.Binding == "" {
		t.Fatal("OAuth state or browser binding is missing")
	}
	pending := service.states[state]
	expectedVerifier = pending.Verifier
	challenge := sha256.Sum256([]byte(expectedVerifier))
	if authorizationURL.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || authorizationURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("invalid PKCE challenge: %v", authorizationURL.Query())
	}
	callback := OAuthCallback{
		Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user", State: state,
		Code: "authorization-code", Binding: session.Binding,
	}
	connection, err := service.CompleteOAuth(context.Background(), callback)
	if err != nil {
		t.Fatal(err)
	}
	if connection.Status != models.IntegrationConnected || connection.ExternalAccountID != "account-1" || connection.AccessTokenEncrypted != "" || connection.RefreshTokenEncrypted != "" {
		t.Fatalf("public connection leaked tokens or has wrong status: %+v", connection)
	}
	stored := store.connections[connection.ID]
	if stored.AccessTokenEncrypted == "" || strings.Contains(stored.AccessTokenEncrypted, "new-access-token") || strings.Contains(stored.RefreshTokenEncrypted, "new-refresh-token") {
		t.Fatalf("provider tokens were not encrypted: %+v", stored)
	}
	accessToken, err := service.cipher.Open(connectionTokenContext(stored.ID, "access"), stored.AccessTokenEncrypted)
	if err != nil || accessToken != "new-access-token" {
		t.Fatalf("decrypt access token: %q, %v", accessToken, err)
	}
	if stored.TokenExpiresAt == nil || !stored.TokenExpiresAt.Equal(service.nowUTC().Add(time.Hour)) {
		t.Fatalf("token expiry was not retained: %v", stored.TokenExpiresAt)
	}
	if _, err := service.CompleteOAuth(context.Background(), callback); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("OAuth state replay succeeded: %v", err)
	}
	if tokenCalls != 1 {
		t.Fatalf("token endpoint called %d times, want 1", tokenCalls)
	}

	second, err := service.BeginOAuth(context.Background(), OAuthRequest{Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	secondURL, _ := url.Parse(second.AuthorizationURL)
	if _, err := service.CompleteOAuth(context.Background(), OAuthCallback{
		Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user",
		State: secondURL.Query().Get("state"), Code: "authorization-code", Binding: "wrong-binding",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid browser binding succeeded: %v", err)
	}
}

func TestUnconfiguredProviderFailsClearly(t *testing.T) {
	service := testService(t, newFakeStore(), &fakeQueue{}, "", "")
	if _, err := service.BeginOAuth(context.Background(), OAuthRequest{Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("disabled provider did not return ErrNotConfigured: %v", err)
	}
	if status := HTTPStatus(ErrNotConfigured); status != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured HTTP status = %d", status)
	}
}

func TestDisconnectRevokesAndClearsLocalCredentials(t *testing.T) {
	store := newFakeStore()
	queue := &fakeQueue{}
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/revoke" {
			http.NotFound(writer, request)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		revoked = request.Form.Get("token") == "access-token"
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	service := testService(t, store, queue, ProviderCanva, server.URL)
	connection := seedConnection(t, service, store, ProviderCanva, false)
	result, err := service.Disconnect(context.Background(), "workspace", "user", ProviderCanva)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Disconnected || !result.ProviderRevoked || !revoked || result.Warning != "" {
		t.Fatalf("unexpected disconnect result: %+v, revoked=%v", result, revoked)
	}
	stored := store.connections[connection.ID]
	if stored.Status != models.IntegrationDisconnected || stored.AccessTokenEncrypted != "" || stored.RefreshTokenEncrypted != "" || stored.DisconnectedAt == nil {
		t.Fatalf("local credentials were not cleared: %+v", stored)
	}
}

func TestWebhookSecretEncryptionAndEventQueue(t *testing.T) {
	store := newFakeStore()
	service := testService(t, store, &fakeQueue{}, "", "")
	created, err := service.CreateWebhook(context.Background(), "workspace", "https://example.com/hooks", []string{"Integration.Export.Queued", "integration.test"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Secret == "" || created.Subscription.SecretCiphertext != "" {
		t.Fatalf("unexpected public webhook result: %+v", created)
	}
	stored := store.webhooks[created.Subscription.ID]
	if stored.SecretCiphertext == "" || strings.Contains(stored.SecretCiphertext, created.Secret) {
		t.Fatalf("webhook secret was not encrypted: %+v", stored)
	}
	var resolver worker.WebhookSecretResolver = service.SigningSecretResolver()
	secret, err := resolver(stored)
	if err != nil || secret != created.Secret {
		t.Fatalf("webhook secret resolution failed: %q, %v", secret, err)
	}
	listed, err := service.Webhooks(context.Background(), "workspace")
	if err != nil || len(listed) != 1 || listed[0].SecretCiphertext != "" {
		t.Fatalf("webhook listing exposed ciphertext: %+v, %v", listed, err)
	}
	queued, err := service.EmitWebhook(context.Background(), "workspace", "integration.export.queued", "event-1", map[string]any{"flipbook_id": "book-1"})
	if err != nil || queued != 1 {
		t.Fatalf("queue webhook: %d, %v", queued, err)
	}
	if len(store.deliveries) != 1 {
		t.Fatalf("delivery count = %d", len(store.deliveries))
	}
	for _, delivery := range store.deliveries {
		if delivery.EventID != "event-1" || delivery.EventType != "integration.export.queued" || delivery.IdempotencyKey != "event-1" || delivery.MaxAttempts != 8 {
			t.Fatalf("incorrect delivery: %+v", delivery)
		}
		var envelope WebhookEvent
		if err := json.Unmarshal([]byte(delivery.PayloadJSON), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID != "event-1" || envelope.WorkspaceID != "workspace" || envelope.Data["flipbook_id"] != "book-1" {
			t.Fatalf("incorrect webhook envelope: %+v", envelope)
		}
	}
	if err := service.SetWebhookEnabled(context.Background(), "workspace", created.Subscription.ID, false); err != nil {
		t.Fatal(err)
	}
	queued, err = service.EmitWebhook(context.Background(), "workspace", "integration.export.queued", "event-2", nil)
	if err != nil || queued != 0 {
		t.Fatalf("disabled subscription received an event: %d, %v", queued, err)
	}
	if _, err := service.CreateWebhook(context.Background(), "workspace", "http://example.com/insecure", []string{"*"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("insecure webhook URL was accepted: %v", err)
	}
}
