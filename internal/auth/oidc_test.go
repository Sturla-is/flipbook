package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/config"
	"github.com/metavert-io/flipbook/internal/models"
	"golang.org/x/oauth2"
)

type mockOIDCProvider struct {
	server       *httptest.Server
	key          *rsa.PrivateKey
	kid          string
	challenge    string
	nonce        string
	binding      string
	subject      string
	email        string
	groups       []string
	amr          []string
	wrongNonce   bool
	wrongKey     bool
	tokenCalls   int
	verifiedMail bool
}

func newMockOIDCProvider(t *testing.T) *mockOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockOIDCProvider{
		key: key, kid: "test-key", subject: "oidc-subject", email: "oidc.user@example.com",
		groups: []string{"publishers"}, amr: []string{"mfa"}, verifiedMail: true,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", provider.discovery)
	mux.HandleFunc("/jwks", provider.jwks)
	mux.HandleFunc("/token", provider.token)
	provider.server = httptest.NewServer(mux)
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *mockOIDCProvider) discovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize",
		"token_endpoint": p.server.URL + "/token", "jwks_uri": p.server.URL + "/jwks",
		"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *mockOIDCProvider) jwks(w http.ResponseWriter, _ *http.Request) {
	exponent := big.NewInt(int64(p.key.PublicKey.E)).Bytes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": p.kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(p.key.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(exponent),
	}}})
}

func (p *mockOIDCProvider) signedToken(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"alg":"RS256","kid":%q,"typ":"JWT"}`, p.kid)))
	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	digest := sha256.Sum256([]byte(header + "." + payload))
	key := p.key
	if p.wrongKey {
		key, _ = rsa.GenerateKey(rand.Reader, 2048)
	}
	signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (p *mockOIDCProvider) token(w http.ResponseWriter, r *http.Request) {
	p.tokenCalls++
	if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "valid-code" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	username, password, basicAuth := r.BasicAuth()
	if !basicAuth || username != "client" || password != "secret" {
		http.Error(w, "invalid client", http.StatusUnauthorized)
		return
	}
	challenge := oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier"))
	if p.challenge == "" || challenge != p.challenge {
		http.Error(w, "invalid PKCE verifier", http.StatusBadRequest)
		return
	}
	nonce := p.nonce
	if p.wrongNonce {
		nonce = "wrong-nonce"
	}
	now := time.Now().UTC()
	claims := map[string]any{
		"iss": p.server.URL, "sub": p.subject, "aud": "client", "exp": now.Add(5 * time.Minute).Unix(),
		"iat": now.Add(-time.Minute).Unix(), "nonce": nonce, "email": p.email,
		"email_verified": p.verifiedMail, "name": "OIDC User", "groups": p.groups,
	}
	if len(p.amr) > 0 {
		claims["amr"] = p.amr
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "access-token", "token_type": "Bearer", "expires_in": 300,
		"id_token": p.signedToken(claims),
	})
}

func oidcOptions(provider *mockOIDCProvider) Options {
	return Options{OIDC: config.OIDCConfig{
		Enabled: true, IssuerURL: provider.server.URL, ClientID: "client", ClientSecret: "secret",
		RedirectURL: "https://example.com/oidc/callback", Scopes: []string{"openid", "profile", "email"},
		GroupClaim: "groups", GroupRoles: map[string]string{"publishers": "publisher", "admins": "admin"}, RequireMFA: true,
	}}
}

func startOIDCLogin(t *testing.T, a *Auth, provider *mockOIDCProvider) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	a.OIDCLogin(recorder, httptest.NewRequest(http.MethodGet, "/oidc/login?return=%2Fadmin", nil))
	if recorder.Code != http.StatusFound {
		t.Fatalf("OIDC start failed: %d %s", recorder.Code, recorder.Body.String())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		t.Fatalf("OIDC start omitted PKCE: %s", location)
	}
	provider.challenge = query.Get("code_challenge")
	state := query.Get("state")
	pending, ok := a.oidcStates[state]
	if !ok {
		t.Fatal("OIDC state was not stored")
	}
	provider.nonce = pending.Nonce
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == OIDCBindingCookieName {
			provider.binding = cookie.Value
		}
	}
	if provider.binding == "" {
		t.Fatal("OIDC browser binding cookie was not set")
	}
	return state
}

func oidcCallback(provider *mockOIDCProvider, state string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/oidc/callback?state="+url.QueryEscape(state)+"&code=valid-code", nil)
	r.AddCookie(&http.Cookie{Name: OIDCBindingCookieName, Value: provider.binding})
	return r
}

func TestOIDCDiscoveryJWKSLoginAndGroupRoleMapping(t *testing.T) {
	provider := newMockOIDCProvider(t)
	store := newFakeStore()
	a := testAuth(t, store, oidcOptions(provider))
	state := startOIDCLogin(t, a, provider)
	recorder := httptest.NewRecorder()
	a.OIDCCallback(recorder, oidcCallback(provider, state))
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("OIDC callback failed: %d %s", recorder.Code, recorder.Body.String())
	}
	user, err := store.GetUserByEmail(context.Background(), provider.email)
	if err != nil {
		t.Fatal(err)
	}
	if user.OIDCIssuer != provider.server.URL || user.OIDCSubject != provider.subject {
		t.Fatalf("OIDC identity was not persisted: %+v", user)
	}
	membership, err := store.GetMembership(context.Background(), models.DefaultWorkspaceID, user.ID)
	if err != nil || membership.Role != models.RolePublisher {
		t.Fatalf("OIDC group was not mapped to publisher: %+v %v", membership, err)
	}
	if provider.tokenCalls != 1 {
		t.Fatalf("unexpected token calls: %d", provider.tokenCalls)
	}
	recorder = httptest.NewRecorder()
	a.OIDCCallback(recorder, oidcCallback(provider, state))
	if recorder.Code != http.StatusBadRequest || provider.tokenCalls != 1 {
		t.Fatalf("OIDC state replay was accepted: %d token calls %d", recorder.Code, provider.tokenCalls)
	}
}

func TestOIDCRejectsBadNonceSignatureAndMissingMFA(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*mockOIDCProvider)
		wantStatus int
	}{
		{"nonce", func(p *mockOIDCProvider) { p.wrongNonce = true }, http.StatusUnauthorized},
		{"signature", func(p *mockOIDCProvider) { p.wrongKey = true }, http.StatusUnauthorized},
		{"mfa", func(p *mockOIDCProvider) { p.amr = nil }, http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := newMockOIDCProvider(t)
			test.configure(provider)
			a := testAuth(t, newFakeStore(), oidcOptions(provider))
			state := startOIDCLogin(t, a, provider)
			recorder := httptest.NewRecorder()
			a.OIDCCallback(recorder, oidcCallback(provider, state))
			if recorder.Code != test.wantStatus {
				t.Fatalf("OIDC rejection returned %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestOIDCDoesNotAutoLinkExistingEmail(t *testing.T) {
	provider := newMockOIDCProvider(t)
	store := newFakeStore()
	addUser(t, store, provider.email, testPassword, models.RoleEditor)
	a := testAuth(t, store, oidcOptions(provider))
	state := startOIDCLogin(t, a, provider)
	recorder := httptest.NewRecorder()
	a.OIDCCallback(recorder, oidcCallback(provider, state))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("existing email was silently linked: %d %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == SessionCookieName && cookie.Value != "" {
			t.Fatal("account-linking failure created a session")
		}
	}
}

func TestOIDCStateIsRequired(t *testing.T) {
	provider := newMockOIDCProvider(t)
	a := testAuth(t, newFakeStore(), oidcOptions(provider))
	recorder := httptest.NewRecorder()
	a.OIDCCallback(recorder, httptest.NewRequest(http.MethodGet, "/oidc/callback?state=unknown&code=valid-code", nil))
	if recorder.Code != http.StatusBadRequest || provider.tokenCalls != 0 {
		t.Fatalf("unknown OIDC state reached token exchange: %d calls %d", recorder.Code, provider.tokenCalls)
	}
	if !strings.Contains(recorder.Body.String(), "invalid or expired") {
		t.Fatal("invalid state did not render a safe error")
	}
}
