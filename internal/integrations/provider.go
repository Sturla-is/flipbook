package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jonradoff/flipbook/internal/models"
)

const maxProviderResponseBytes = 1 << 20

type providerClient struct {
	provider string
	config   ProviderConfig
	service  *Service
}

func (s *Service) newProviderClient(provider string, config ProviderConfig) *providerClient {
	return &providerClient{provider: provider, config: config, service: s}
}

func secureProviderURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

func validateProviderEndpoints(config ProviderConfig) error {
	if !config.Configured() {
		return integrationError(ErrNotConfigured, "", "configuration", "Provider OAuth and API endpoints are not fully configured.")
	}
	for _, candidate := range []string{config.AuthorizationURL, config.TokenURL, config.RedirectURL, config.APIBaseURL} {
		if !secureProviderURL(candidate) {
			return integrationError(ErrNotConfigured, "", "configuration", "Provider and redirect URLs must use HTTPS, except for loopback development URLs.")
		}
	}
	if config.RevocationURL != "" && !secureProviderURL(config.RevocationURL) {
		return integrationError(ErrNotConfigured, "", "configuration", "The provider revocation URL must use HTTPS, except for loopback development URLs.")
	}
	return nil
}

func sameURLOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func resolveProviderURL(config ProviderConfig, reference string) (string, error) {
	if strings.TrimSpace(reference) == "" {
		return "", integrationError(ErrNotConfigured, "", "api", "The provider API endpoint is not configured for this operation.")
	}
	base, err := url.Parse(config.APIBaseURL)
	if err != nil {
		return "", integrationError(ErrNotConfigured, "", "api", "The provider API base URL is invalid.")
	}
	referenceURL, err := url.Parse(reference)
	if err != nil {
		return "", integrationError(ErrNotConfigured, "", "api", "The provider API endpoint is invalid.")
	}
	if !referenceURL.IsAbs() && !strings.HasPrefix(reference, "/") {
		return "", integrationError(ErrNotConfigured, "", "api", "Provider API paths must begin with a slash.")
	}
	resolved := base.ResolveReference(referenceURL)
	if !secureProviderURL(resolved.String()) || !sameURLOrigin(base, resolved) || resolved.User != nil {
		return "", integrationError(ErrNotConfigured, "", "api", "Authenticated provider endpoints must remain on the configured HTTPS API origin.")
	}
	return resolved.String(), nil
}

func expandProviderPath(path, key, value string) string {
	return strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(value))
}

type tokenResponse struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	TokenType         string `json:"token_type"`
	Scope             string `json:"scope"`
	ExpiresIn         int64  `json:"expires_in"`
	UserID            string `json:"user_id"`
	AccountID         string `json:"account_id"`
	Subject           string `json:"sub"`
	ExternalAccountID string `json:"-"`
}

func (t tokenResponse) ExternalID() string {
	for _, value := range []string{t.UserID, t.AccountID, t.Subject} {
		if value != "" {
			return value
		}
	}
	return ""
}

func (t tokenResponse) ExpiresAt(now time.Time) *time.Time {
	if t.ExpiresIn <= 0 {
		return nil
	}
	expires := now.Add(time.Duration(t.ExpiresIn) * time.Second)
	return &expires
}

func (t tokenResponse) ScopeList(fallback []string) []string {
	if strings.TrimSpace(t.Scope) == "" {
		return append([]string(nil), fallback...)
	}
	return strings.Fields(t.Scope)
}

func (c *providerClient) exchangeCode(ctx context.Context, code, verifier string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.config.RedirectURL},
		"code_verifier": {verifier},
	}
	return c.requestToken(ctx, "authorize", form)
}

func (c *providerClient) refreshToken(ctx context.Context, refreshToken string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	return c.requestToken(ctx, "refresh token", form)
}

func (c *providerClient) requestToken(ctx context.Context, operation string, form url.Values) (*tokenResponse, error) {
	if err := validateProviderEndpoints(c.config); err != nil {
		return nil, err
	}
	if c.provider == ProviderAdobeExpress {
		form.Set("client_id", c.config.ClientID)
		form.Set("client_secret", c.config.ClientSecret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if c.provider == ProviderCanva {
		request.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)
	}
	response, err := c.service.opts.HTTPClient.Do(request)
	if err != nil {
		return nil, integrationError(ErrProvider, c.provider, operation, providerDisplayName(c.provider)+" token request failed. Check the provider endpoint and try again.")
	}
	defer response.Body.Close()
	body, err := readProviderBody(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerResponseError(c.provider, operation, response.StatusCode, body)
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return nil, integrationError(ErrProvider, c.provider, operation, providerDisplayName(c.provider)+" returned an invalid token response.")
	}
	token.ExternalAccountID = token.ExternalID()
	return &token, nil
}

func readProviderBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxProviderResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxProviderResponseBytes {
		return nil, integrationError(ErrProvider, "", "response", "The provider response exceeded the safety limit.")
	}
	return body, nil
}

func cleanProviderMessage(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 300 {
		value = value[:300] + "…"
	}
	return value
}

func providerResponseError(provider, operation string, status int, body []byte) error {
	var payload struct {
		Code             string          `json:"code"`
		Message          string          `json:"message"`
		Error            json.RawMessage `json:"error"`
		ErrorDescription string          `json:"error_description"`
	}
	_ = json.Unmarshal(body, &payload)
	code := payload.Code
	message := payload.Message
	if message == "" {
		message = payload.ErrorDescription
	}
	if len(payload.Error) > 0 {
		var text string
		if json.Unmarshal(payload.Error, &text) == nil {
			if code == "" {
				code = text
			}
		} else {
			var nested struct {
				Code        string `json:"code"`
				Message     string `json:"message"`
				Description string `json:"description"`
			}
			if json.Unmarshal(payload.Error, &nested) == nil {
				if code == "" {
					code = nested.Code
				}
				if message == "" {
					message = nested.Message
				}
				if message == "" {
					message = nested.Description
				}
			}
		}
	}
	kind := ErrProvider
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = ErrPermission
	case http.StatusNotFound:
		kind = ErrNotFound
	case http.StatusTooManyRequests:
		kind = ErrRateLimited
	case http.StatusConflict:
		kind = ErrConflict
	}
	lowerCode := strings.ToLower(code)
	if strings.Contains(lowerCode, "permission") || strings.Contains(lowerCode, "approval") || strings.Contains(lowerCode, "license") || strings.Contains(lowerCode, "access_denied") {
		kind = ErrPermission
	}
	detail := providerDisplayName(provider) + " " + operation + " failed"
	if status > 0 {
		detail += " (HTTP " + strconv.Itoa(status) + ")"
	}
	if code != "" {
		detail += ": " + cleanProviderMessage(code)
	} else {
		detail += "."
	}
	if kind == ErrPermission {
		detail += " Check app approval, OAuth scopes, and document sharing."
	} else if kind == ErrNotFound {
		detail += " Check the external document ID."
	}
	if message = cleanProviderMessage(message); message != "" {
		detail += " Provider response: " + message
	}
	return &Error{Kind: kind, Provider: provider, Operation: operation, StatusCode: status, Message: detail}
}

func (c *providerClient) doJSON(ctx context.Context, connection *models.IntegrationConnection, method, reference, operation string, input, output any) error {
	if connection == nil {
		return integrationError(ErrNotConnected, c.provider, operation, "A provider connection is required.")
	}
	accessToken, err := c.service.accessToken(ctx, connection)
	if err != nil {
		return err
	}
	endpoint, err := resolveProviderURL(c.config, reference)
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if c.provider == ProviderAdobeExpress {
		request.Header.Set("X-Api-Key", c.config.ClientID)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.service.opts.HTTPClient.Do(request)
	if err != nil {
		return integrationError(ErrProvider, c.provider, operation, providerDisplayName(c.provider)+" "+operation+" request failed. Check network access and try again.")
	}
	defer response.Body.Close()
	responseBody, err := readProviderBody(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerResponseError(c.provider, operation, response.StatusCode, responseBody)
	}
	if output == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return integrationError(ErrProvider, c.provider, operation, providerDisplayName(c.provider)+" returned an invalid JSON response for "+operation+".")
	}
	return nil
}

func (c *providerClient) verify(ctx context.Context, connection *models.IntegrationConnection) error {
	if strings.TrimSpace(c.config.VerifyPath) == "" {
		return integrationError(ErrNotConfigured, c.provider, "verify connection", "The provider verification endpoint is not configured.")
	}
	return c.doJSON(ctx, connection, http.MethodGet, c.config.VerifyPath, "verify connection", nil, nil)
}

func (c *providerClient) revoke(ctx context.Context, accessToken string) error {
	if c.config.RevocationURL == "" {
		return nil
	}
	if !secureProviderURL(c.config.RevocationURL) {
		return integrationError(ErrNotConfigured, c.provider, "revoke", "The provider revocation endpoint is invalid.")
	}
	form := url.Values{"token": {accessToken}, "client_id": {c.config.ClientID}, "client_secret": {c.config.ClientSecret}}
	if c.provider == ProviderCanva {
		form.Del("client_secret")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.RevocationURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if c.provider == ProviderCanva {
		request.SetBasicAuth(c.config.ClientID, c.config.ClientSecret)
	}
	response, err := c.service.opts.HTTPClient.Do(request)
	if err != nil {
		return integrationError(ErrProvider, c.provider, "revoke", "The provider revocation request failed.")
	}
	defer response.Body.Close()
	body, err := readProviderBody(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerResponseError(c.provider, "revoke", response.StatusCode, body)
	}
	return nil
}

func (c *providerClient) download(ctx context.Context, rawURL string) (*http.Response, error) {
	if !secureProviderURL(rawURL) {
		return nil, integrationError(ErrProvider, c.provider, "download export", "The provider returned an unsafe export download URL.")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/pdf,application/octet-stream")
	response, err := c.service.opts.DownloadClient.Do(request)
	if err != nil {
		return nil, integrationError(ErrProvider, c.provider, "download export", "The exported PDF could not be downloaded.")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, readErr := readProviderBody(response.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, providerResponseError(c.provider, "download export", response.StatusCode, body)
	}
	if response.ContentLength > c.service.opts.MaxDownloadBytes {
		response.Body.Close()
		return nil, integrationError(ErrInvalidInput, c.provider, "download export", fmt.Sprintf("The exported PDF exceeds the %d byte download limit.", c.service.opts.MaxDownloadBytes))
	}
	return response, nil
}
