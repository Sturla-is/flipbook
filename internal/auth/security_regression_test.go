package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
)

func TestForwardedRequestCannotUseLocalBootstrap(t *testing.T) {
	a := testAuth(t, newFakeStore(), Options{})
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.50")
	recorder := httptest.NewRecorder()
	a.LoginPage(recorder, r)
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "Create administrator") {
		t.Fatalf("forwarded remote request reached local bootstrap: %d", recorder.Code)
	}
}

func TestLegacyCLIPasswordChangeRevokesSessions(t *testing.T) {
	store := newFakeStore()
	oldHash, err := hashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	store.settings[settingPwdHash] = oldHash
	a := testAuth(t, store, Options{})
	recorder := httptest.NewRecorder()
	if _, err := a.LoginWithCredentials(context.Background(), recorder, "admin", testPassword); err != nil {
		t.Fatal(err)
	}
	var sessionToken string
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == SessionCookieName {
			sessionToken = cookie.Value
		}
	}
	newPassword := "legacy-new-password"
	newHash, err := hashPassword(newPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(settingPwdHash, newHash); err != nil {
		t.Fatal(err)
	}
	if _, err := a.principalFromRequest(authenticatedRequest(sessionToken, "/admin")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy CLI password change left session active: %v", err)
	}
	if _, err := a.LoginWithCredentials(context.Background(), httptest.NewRecorder(), "admin", testPassword); !errors.Is(err, errAuthenticationFailed) {
		t.Fatalf("old legacy password remained valid: %v", err)
	}
	if _, err := a.LoginWithCredentials(context.Background(), httptest.NewRecorder(), "admin", newPassword); err != nil {
		t.Fatalf("new legacy password did not migrate and authenticate: %v", err)
	}
}

func TestExpiredAndDisabledUserSessionsAreDenied(t *testing.T) {
	store := newFakeStore()
	user := addUser(t, store, "editor@example.com", testPassword, models.RoleEditor)
	a := testAuth(t, store, Options{})
	expiredToken := sessionForUser(t, a, user, models.DefaultWorkspaceID)
	store.sessions[models.HashToken(expiredToken)].ExpiresAt = time.Now().Add(-time.Minute)
	if _, err := a.principalFromRequest(authenticatedRequest(expiredToken, "/admin")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session remained valid: %v", err)
	}
	disabledToken := sessionForUser(t, a, user, models.DefaultWorkspaceID)
	now := time.Now().UTC()
	store.users[user.ID].DisabledAt = &now
	if _, err := a.principalFromRequest(authenticatedRequest(disabledToken, "/admin")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled user's session remained valid: %v", err)
	}
}

func TestAPIKeyScopeFollowsCurrentRoleAndExpiry(t *testing.T) {
	store := newFakeStore()
	admin := addUser(t, store, "admin@example.com", testPassword, models.RoleAdmin)
	a := testAuth(t, store, Options{})
	secret, key, err := a.IssueAPIKey(context.Background(), models.DefaultWorkspaceID, admin.ID, "Automation", []string{models.ScopeFlipbooksWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMembership(context.Background(), &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: admin.ID, Role: models.RoleReader}); err != nil {
		t.Fatal(err)
	}
	handler := a.RequireAPIKey(models.ScopeFlipbooksWrite)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPost, "/api/flipbooks", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("demoted user's key retained write access: %d", recorder.Code)
	}
	if err := store.UpsertMembership(context.Background(), &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: admin.ID, Role: models.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Minute)
	store.apiKeys[key.ID].ExpiresAt = &expired
	r = httptest.NewRequest(http.MethodPost, "/api/flipbooks", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expired key remained valid: %d", recorder.Code)
	}
}

func TestPasswordResetDeliveryDoesNotEnumerateAccounts(t *testing.T) {
	store := newFakeStore()
	user := addUser(t, store, "editor@example.com", testPassword, models.RoleEditor)
	deliveryCalls := 0
	a := testAuth(t, store, Options{PasswordResetDelivery: func(context.Context, string, string) error {
		deliveryCalls++
		return errors.New("delivery unavailable")
	}})
	post := func(email string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		a.PasswordResetRequestHandler(recorder, formRequest(a, http.MethodPost, "/password-reset", url.Values{"email": {email}}, ""))
		return recorder
	}
	known := post(user.Email)
	unknown := post("unknown@example.com")
	if known.Code != http.StatusOK || unknown.Code != http.StatusOK || known.Body.String() != unknown.Body.String() {
		t.Fatalf("reset delivery revealed account existence: known %d unknown %d", known.Code, unknown.Code)
	}
	if deliveryCalls != 1 {
		t.Fatalf("delivery hook called %d times, want once", deliveryCalls)
	}
	a.opts.PasswordResetDelivery = nil
	if recorder := post(user.Email); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured delivery was not cleanly disabled for known account: %d", recorder.Code)
	}
	if recorder := post("unknown@example.com"); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured delivery was not cleanly disabled for unknown account: %d", recorder.Code)
	}
}

func TestBearerHeaderCannotBypassCSRFForCookieSession(t *testing.T) {
	a := testAuth(t, newFakeStore(), Options{})
	called := false
	handler := a.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	r := httptest.NewRequest(http.MethodPost, "/admin/upload", nil)
	r.Header.Set("Authorization", "Bearer arbitrary")
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "browser-session"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("bearer header bypassed cookie CSRF protection: %d", recorder.Code)
	}

	origin := httptest.NewRequest(http.MethodPost, "https://example.com:443/login", nil)
	origin.Header.Set("Origin", "https://example.com")
	if err := a.validateOrigin(origin); err != nil {
		t.Fatalf("default HTTPS port was not normalized: %v", err)
	}
}

func TestOIDCCallbackRequiresBrowserBinding(t *testing.T) {
	provider := newMockOIDCProvider(t)
	a := testAuth(t, newFakeStore(), oidcOptions(provider))
	state := startOIDCLogin(t, a, provider)
	r := httptest.NewRequest(http.MethodGet, "/oidc/callback?state="+url.QueryEscape(state)+"&code=valid-code", nil)
	recorder := httptest.NewRecorder()
	a.OIDCCallback(recorder, r)
	if recorder.Code != http.StatusBadRequest || provider.tokenCalls != 0 {
		t.Fatalf("OIDC callback without browser binding reached token exchange: %d calls %d", recorder.Code, provider.tokenCalls)
	}
}

func TestProviderURLsRequireHTTPSOffLoopback(t *testing.T) {
	for raw, valid := range map[string]bool{
		"http://127.0.0.1:8080/issuer": true,
		"http://localhost:8080/issuer": true,
		"http://example.com/issuer":    false,
		"https://example.com/issuer":   true,
		"ftp://example.com/issuer":     false,
	} {
		if secureProviderURL(raw) != valid {
			t.Fatalf("secureProviderURL(%q) = %v, want %v", raw, !valid, valid)
		}
	}
}

func TestSessionRevocationAuthorization(t *testing.T) {
	store := newFakeStore()
	target := addUser(t, store, "target@example.com", testPassword, models.RoleEditor)
	other := addUser(t, store, "other@example.com", testPassword, models.RoleEditor)
	a := testAuth(t, store, Options{})
	token := sessionForUser(t, a, target, models.DefaultWorkspaceID)
	if err := a.RevokeUserSessions(context.Background(), models.DefaultWorkspaceID, other.ID, target.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor revoked another user's sessions: %v", err)
	}
	if _, err := a.principalFromRequest(authenticatedRequest(token, "/admin")); err != nil {
		t.Fatalf("failed revocation attempt changed session: %v", err)
	}
	if err := a.RevokeUserSessions(context.Background(), models.DefaultWorkspaceID, models.LegacyAdminUserID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.principalFromRequest(authenticatedRequest(token, "/admin")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("admin revocation did not invalidate target session: %v", err)
	}
}

func TestInvitationRejectsRoleEscalation(t *testing.T) {
	store := newFakeStore()
	editor := addUser(t, store, "editor@example.com", testPassword, models.RoleEditor)
	a := testAuth(t, store, Options{})
	if _, _, err := a.CreateInvitation(context.Background(), models.DefaultWorkspaceID, models.LegacyAdminUserID, "owner@example.com", models.RoleOwner, time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner invitation was allowed: %v", err)
	}
	if _, _, err := a.CreateInvitation(context.Background(), models.DefaultWorkspaceID, editor.ID, "reader@example.com", models.RoleReader, time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor created an invitation: %v", err)
	}
}
