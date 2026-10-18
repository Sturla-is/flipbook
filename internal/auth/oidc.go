package auth

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"golang.org/x/oauth2"
)

type oidcRuntime struct {
	provider *gooidc.Provider
	verifier *gooidc.IDTokenVerifier
	oauth2   oauth2.Config
}

func (a *Auth) OIDCEnabled() bool {
	return a != nil && a.initErr == nil && a.opts.OIDC.Configured()
}

func (a *Auth) oidc(ctx context.Context) (*oidcRuntime, error) {
	if !a.OIDCEnabled() {
		return nil, ErrNotConfigured
	}
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	if a.oidcProvider != nil {
		return a.oidcProvider, nil
	}
	ctx = gooidc.ClientContext(ctx, a.httpClient)
	provider, err := gooidc.NewProvider(ctx, a.opts.OIDC.IssuerURL)
	if err != nil {
		return nil, err
	}
	scopes := append([]string(nil), a.opts.OIDC.Scopes...)
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	hasOpenID := false
	for _, scope := range scopes {
		if scope == gooidc.ScopeOpenID {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		scopes = append([]string{gooidc.ScopeOpenID}, scopes...)
	}
	runtime := &oidcRuntime{
		provider: provider,
		verifier: provider.Verifier(&gooidc.Config{ClientID: a.opts.OIDC.ClientID}),
		oauth2: oauth2.Config{
			ClientID: a.opts.OIDC.ClientID, ClientSecret: a.opts.OIDC.ClientSecret,
			RedirectURL: a.opts.OIDC.RedirectURL, Endpoint: provider.Endpoint(), Scopes: scopes,
		},
	}
	a.oidcProvider = runtime
	return runtime, nil
}

func (a *Auth) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	runtime, err := a.oidc(r.Context())
	if err != nil {
		http.Error(w, "OpenID Connect is unavailable.", http.StatusServiceUnavailable)
		return
	}
	state, err := randomToken(a.random, 32)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	nonce, err := randomToken(a.random, 32)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	verifier, err := randomToken(a.random, 32)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	binding := ""
	if cookie, cookieErr := r.Cookie(OIDCBindingCookieName); cookieErr == nil && validCSRFCookie(cookie.Value) {
		binding = cookie.Value
	}
	if binding == "" {
		binding, err = randomToken(a.random, 32)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
	}
	now := a.now()
	http.SetCookie(w, &http.Cookie{
		Name: OIDCBindingCookieName, Value: binding, Path: "/", HttpOnly: true,
		Secure: a.opts.SecureCookies || a.opts.HTTPSMode, SameSite: http.SameSiteLaxMode,
		Expires: now.Add(10 * time.Minute), MaxAge: 600,
	})
	a.oidcMu.Lock()
	for key, pending := range a.oidcStates {
		if !now.Before(pending.ExpiresAt) {
			delete(a.oidcStates, key)
		}
	}
	if len(a.oidcStates) >= 10000 {
		a.oidcMu.Unlock()
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	a.oidcStates[state] = oidcState{Nonce: nonce, Verifier: verifier, Binding: binding, ReturnTo: safeReturnTo(r.URL.Query().Get("return")), ExpiresAt: now.Add(10 * time.Minute)}
	a.oidcMu.Unlock()
	url := runtime.oauth2.AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier), oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *Auth) consumeOIDCState(state string) (oidcState, bool) {
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	pending, ok := a.oidcStates[state]
	delete(a.oidcStates, state)
	if !ok || !a.now().Before(pending.ExpiresAt) {
		return oidcState{}, false
	}
	return pending, true
}

func (a *Auth) renderOIDCError(w http.ResponseWriter, r *http.Request, message string, status int) {
	data, err := a.baseLoginData(w, r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	data.Error = message
	a.renderLogin(w, r, status, data)
}

func claimValue(claims map[string]any, path string) any {
	var value any = claims
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[part]
	}
	return value
}

func claimStrings(claims map[string]any, path string) []string {
	switch value := claimValue(claims, path).(type) {
	case string:
		if value != "" {
			return []string{value}
		}
	case []string:
		return value
	case []any:
		values := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && text != "" {
				values = append(values, text)
			}
		}
		return values
	}
	return nil
}

func claimString(claims map[string]any, path string) string {
	values := claimStrings(claims, path)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func hasMFA(claims map[string]any) bool {
	for _, method := range claimStrings(claims, "amr") {
		switch strings.ToLower(method) {
		case "mfa", "otp", "sms", "hwk", "fido", "webauthn", "phr", "push":
			return true
		}
	}
	return false
}

func (a *Auth) oidcRole(claims map[string]any) (models.Role, bool) {
	role := models.RoleReader
	mapped := false
	for _, group := range claimStrings(claims, a.opts.OIDC.GroupClaim) {
		configured, ok := a.opts.OIDC.GroupRoles[group]
		if !ok {
			continue
		}
		candidate := models.Role(configured)
		if candidate.Valid() && (!mapped || candidate.AtLeast(role)) {
			role = candidate
			mapped = true
		}
	}
	return role, mapped
}

func (a *Auth) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !a.OIDCEnabled() {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("error") != "" {
		a.renderOIDCError(w, r, "The identity provider did not complete sign-in.", http.StatusUnauthorized)
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	pending, ok := a.consumeOIDCState(state)
	if !ok || code == "" {
		a.renderOIDCError(w, r, "The sign-in state is invalid or expired. Start again.", http.StatusBadRequest)
		return
	}
	binding, err := r.Cookie(OIDCBindingCookieName)
	if err != nil || !hmac.Equal([]byte(binding.Value), []byte(pending.Binding)) {
		a.renderOIDCError(w, r, "The sign-in browser binding is invalid. Start again.", http.StatusBadRequest)
		return
	}
	runtime, err := a.oidc(r.Context())
	if err != nil {
		a.renderOIDCError(w, r, "OpenID Connect is unavailable.", http.StatusServiceUnavailable)
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, a.httpClient)
	token, err := runtime.oauth2.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", pending.Verifier))
	if err != nil {
		a.renderOIDCError(w, r, "The identity provider could not be verified.", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		a.renderOIDCError(w, r, "The identity provider did not return an ID token.", http.StatusUnauthorized)
		return
	}
	idToken, err := runtime.verifier.Verify(ctx, rawIDToken)
	if err != nil || idToken.Nonce == "" || !hmac.Equal([]byte(idToken.Nonce), []byte(pending.Nonce)) {
		a.renderOIDCError(w, r, "The identity token could not be verified.", http.StatusUnauthorized)
		return
	}
	claims := make(map[string]any)
	if err := idToken.Claims(&claims); err != nil {
		a.renderOIDCError(w, r, "The identity claims could not be read.", http.StatusUnauthorized)
		return
	}
	if a.opts.OIDC.RequireMFA && !hasMFA(claims) {
		a.audit(r.Context(), models.DefaultWorkspaceID, "", "", "auth.login", "session", "", "failure", map[string]string{"method": "oidc", "reason": "mfa_required"})
		a.renderOIDCError(w, r, "Multi-factor authentication is required by the identity provider.", http.StatusForbidden)
		return
	}
	principal, err := a.oidcPrincipal(r.Context(), w, idToken.Issuer, idToken.Subject, claims)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountLinkRequired):
			a.renderOIDCError(w, r, "This email belongs to an existing account that must be linked before OIDC sign-in.", http.StatusConflict)
		case errors.Is(err, ErrNoWorkspace), errors.Is(err, ErrForbidden):
			a.renderOIDCError(w, r, "Your identity does not have access to a workspace.", http.StatusForbidden)
		case errors.Is(err, errAuthenticationFailed):
			a.renderOIDCError(w, r, "This account is disabled.", http.StatusForbidden)
		default:
			a.renderOIDCError(w, r, "OIDC account provisioning failed.", http.StatusServiceUnavailable)
		}
		return
	}
	a.audit(r.Context(), principal.WorkspaceID(), principal.User.ID, "", "auth.login", "session", "", "success", map[string]string{"method": "oidc"})
	http.Redirect(w, r, pending.ReturnTo, http.StatusSeeOther)
}

func (a *Auth) oidcPrincipal(ctx context.Context, w http.ResponseWriter, issuer, subject string, claims map[string]any) (*Principal, error) {
	if issuer == "" || subject == "" {
		return nil, ErrOIDC
	}
	role, roleMapped := a.oidcRole(claims)
	user, err := a.store.GetUserByOIDC(ctx, issuer, subject)
	isNew := false
	if errors.Is(err, database.ErrNotFound) {
		email := ""
		if verified, _ := claimValue(claims, "email_verified").(bool); verified {
			email = claimString(claims, "email")
		}
		if email != "" {
			if _, lookupErr := a.store.GetUserByEmail(ctx, email); lookupErr == nil {
				return nil, ErrAccountLinkRequired
			} else if !errors.Is(lookupErr, database.ErrNotFound) {
				return nil, lookupErr
			}
		}
		name := claimString(claims, "name")
		if name == "" {
			name = "OIDC user"
		}
		user = &models.User{
			Email: email, Name: name, Locale: claimString(claims, "locale"),
			OIDCIssuer: issuer, OIDCSubject: subject,
		}
		if err := a.store.CreateUser(ctx, user); err != nil {
			return nil, err
		}
		isNew = true
	} else if err != nil {
		return nil, err
	}
	if user == nil || !user.Active() {
		return nil, errAuthenticationFailed
	}
	preferredWorkspace := ""
	if isNew {
		if _, err := a.store.GetWorkspace(ctx, models.DefaultWorkspaceID); err != nil {
			return nil, err
		}
		if err := a.store.UpsertMembership(ctx, &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: user.ID, Role: role}); err != nil {
			return nil, err
		}
		preferredWorkspace = models.DefaultWorkspaceID
	} else {
		name := claimString(claims, "name")
		locale := claimString(claims, "locale")
		if name == "" {
			name = user.Name
		}
		if locale == "" {
			locale = user.Locale
		}
		if err := a.store.UpdateUserProfile(ctx, user.ID, name, locale, user.TimeZone); err != nil {
			return nil, err
		}
		membership, membershipErr := a.store.GetMembership(ctx, models.DefaultWorkspaceID, user.ID)
		if membershipErr == nil {
			preferredWorkspace = models.DefaultWorkspaceID
			if roleMapped && membership.Role != role {
				if err := a.store.UpsertMembership(ctx, &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: user.ID, Role: role}); err != nil {
					return nil, err
				}
			}
		} else if !errors.Is(membershipErr, database.ErrNotFound) {
			return nil, membershipErr
		} else {
			workspaces, err := a.store.ListWorkspacesForUser(ctx, user.ID)
			if err != nil {
				return nil, err
			}
			if len(workspaces) == 0 {
				if err := a.store.UpsertMembership(ctx, &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: user.ID, Role: role}); err != nil {
					return nil, err
				}
				preferredWorkspace = models.DefaultWorkspaceID
			}
		}
	}
	return a.createSessionForUser(ctx, w, user, preferredWorkspace)
}
