package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
)

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(value, "@") {
		return "", errors.New("valid email address is required")
	}
	return value, nil
}

func (a *Auth) managementMembership(ctx context.Context, workspaceID, actorUserID string, required models.Role) (*models.Membership, error) {
	membership, err := a.store.GetMembership(ctx, workspaceID, actorUserID)
	if err != nil {
		return nil, err
	}
	if !membership.Role.AtLeast(required) {
		return nil, ErrForbidden
	}
	return membership, nil
}

func (a *Auth) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if a == nil || a.initErr != nil {
		return ErrNotConfigured
	}
	user, err := a.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if !user.Active() || !passwordMatches(user.PasswordHash, currentPassword) {
		return errAuthenticationFailed
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := a.store.SetUserPassword(ctx, userID, hash); err != nil {
		return err
	}
	a.audit(ctx, models.DefaultWorkspaceID, userID, "", "auth.password.change", "user", userID, "success", nil)
	return nil
}

func (a *Auth) RevokeUserSessions(ctx context.Context, workspaceID, actorUserID, targetUserID string) error {
	if actorUserID != targetUserID {
		if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
			return err
		}
	} else if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleReader); err != nil {
		return err
	}
	if _, err := a.store.GetMembership(ctx, workspaceID, targetUserID); err != nil {
		return err
	}
	if err := a.store.RevokeUserSessions(ctx, targetUserID); err != nil {
		return err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.sessions.revoke_all", "user", targetUserID, "success", nil)
	return nil
}

type passwordResetPayload struct {
	Version     int    `json:"v"`
	UserID      string `json:"sub"`
	ExpiresAt   int64  `json:"exp"`
	Fingerprint string `json:"fp"`
	Nonce       string `json:"nonce"`
}

func passwordFingerprint(user *models.User) string {
	sum := sha256.Sum256([]byte(user.PasswordHash))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (a *Auth) signPasswordReset(payload passwordResetPayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(encoded)
	mac := hmac.New(sha256.New, []byte(a.secret))
	mac.Write([]byte("password-reset\x00"))
	mac.Write([]byte(body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

// RequestPasswordReset returns an empty token when the account does not exist,
// allowing callers to provide a non-enumerating response. The caller must
// deliver the returned token through a trusted channel; it is never logged.
func (a *Auth) RequestPasswordReset(ctx context.Context, email string, ttl time.Duration) (string, error) {
	if a == nil || a.initErr != nil {
		return "", ErrNotConfigured
	}
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	if ttl < 0 || ttl > 24*time.Hour {
		return "", ErrInvalidToken
	}
	user, err := a.store.GetUserByEmail(ctx, email)
	if errors.Is(err, database.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !user.Active() {
		return "", nil
	}
	nonce, err := randomToken(a.random, 16)
	if err != nil {
		return "", err
	}
	return a.signPasswordReset(passwordResetPayload{
		Version: 1, UserID: user.ID, ExpiresAt: a.now().Add(ttl).Unix(),
		Fingerprint: passwordFingerprint(user), Nonce: nonce,
	})
}

func (a *Auth) parsePasswordReset(ctx context.Context, token string) (*models.User, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, []byte(a.secret))
	mac.Write([]byte("password-reset\x00"))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, ErrInvalidToken
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var payload passwordResetPayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, ErrInvalidToken
	}
	if payload.Version != 1 || payload.UserID == "" || payload.Nonce == "" || a.now().Unix() >= payload.ExpiresAt {
		return nil, ErrInvalidToken
	}
	user, err := a.store.GetUser(ctx, payload.UserID)
	if err != nil || !user.Active() || !hmac.Equal([]byte(passwordFingerprint(user)), []byte(payload.Fingerprint)) {
		return nil, ErrInvalidToken
	}
	return user, nil
}

func (a *Auth) ResetPassword(ctx context.Context, token, newPassword string) (*models.User, error) {
	if a == nil || a.initErr != nil {
		return nil, ErrNotConfigured
	}
	user, err := a.parsePasswordReset(ctx, token)
	if err != nil {
		return nil, err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetUserPassword(ctx, user.ID, hash); err != nil {
		return nil, err
	}
	a.audit(ctx, models.DefaultWorkspaceID, user.ID, "", "auth.password.reset", "user", user.ID, "success", nil)
	return user, nil
}

// CreateLocalUser provisions a named local account with a password and a
// workspace membership directly from the admin panel, without the invitation
// flow. Only workspace admins may create accounts; the owner role is never
// assignable through this method.
func (a *Auth) CreateLocalUser(ctx context.Context, workspaceID, actorUserID, name, email, password string, role models.Role) (*models.User, error) {
	if a == nil || a.initErr != nil {
		return nil, ErrNotConfigured
	}
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return nil, err
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return nil, errors.New("account name is required and must be at most 200 characters")
	}
	if !role.Valid() || role == models.RoleOwner {
		return nil, ErrForbidden
	}
	if _, err := a.store.GetUserByEmail(ctx, email); err == nil {
		return nil, ErrAccountExists
	} else if !errors.Is(err, database.ErrNotFound) {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &models.User{Email: email, Name: name, PasswordHash: hash, Locale: "en"}
	if err := a.store.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	membership := &models.Membership{WorkspaceID: workspaceID, UserID: user.ID, Role: role, InvitedBy: actorUserID}
	if err := a.store.UpsertMembership(ctx, membership); err != nil {
		return nil, err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.user.create", "user", user.ID, "success", map[string]string{"role": string(role)})
	return user, nil
}

// SetUserPasswordByAdmin resets the local password of a workspace member from
// the admin panel. It revokes every session of the target user. Only workspace
// admins may reset passwords, and an owner's password can only be reset by an
// owner.
func (a *Auth) SetUserPasswordByAdmin(ctx context.Context, workspaceID, actorUserID, targetUserID, newPassword string) error {
	if a == nil || a.initErr != nil {
		return ErrNotConfigured
	}
	actorMembership, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin)
	if err != nil {
		return err
	}
	targetMembership, err := a.store.GetMembership(ctx, workspaceID, targetUserID)
	if err != nil {
		return err
	}
	if targetMembership.Role == models.RoleOwner && actorMembership.Role != models.RoleOwner {
		return ErrForbidden
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := a.store.SetUserPassword(ctx, targetUserID, hash); err != nil {
		return err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.password.admin_set", "user", targetUserID, "success", nil)
	return nil
}

// SetLegacyAdminPassword rotates the primary legacy "admin" login password
// from the admin panel, replacing the CLI-only set-password flow for signed-in
// admins. It revokes every session of the legacy admin user.
func (a *Auth) SetLegacyAdminPassword(ctx context.Context, workspaceID, actorUserID, newPassword string) error {
	if a == nil || a.initErr != nil {
		return ErrNotConfigured
	}
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return err
	}
	if err := a.SetPassword(newPassword); err != nil {
		return err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.password.admin_set", "user", models.LegacyAdminUserID, "success", nil)
	return nil
}

func (a *Auth) CreateInvitation(ctx context.Context, workspaceID, actorUserID, email string, role models.Role, ttl time.Duration) (string, *models.Invitation, error) {
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return "", nil, err
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return "", nil, err
	}
	if !role.Valid() || role == models.RoleOwner {
		return "", nil, ErrForbidden
	}
	if ttl == 0 {
		ttl = 7 * 24 * time.Hour
	}
	if ttl < 0 || ttl > 30*24*time.Hour {
		return "", nil, ErrInvalidToken
	}
	token, err := randomToken(a.random, 32)
	if err != nil {
		return "", nil, err
	}
	invitation := &models.Invitation{
		WorkspaceID: workspaceID, Email: email, Role: role,
		TokenHash: models.HashToken(token), InvitedBy: actorUserID, ExpiresAt: a.now().Add(ttl),
	}
	if err := a.store.CreateInvitation(ctx, invitation); err != nil {
		return "", nil, err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.invitation.create", "invitation", invitation.ID, "success", map[string]string{"role": string(role)})
	return token, invitation, nil
}

func (a *Auth) AcceptInvitation(ctx context.Context, token, userID string) (*models.Invitation, error) {
	invitation, err := a.store.GetInvitationByToken(ctx, token)
	if errors.Is(err, database.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if err := a.store.AcceptInvitation(ctx, token, userID); err != nil {
		return nil, err
	}
	a.audit(ctx, invitation.WorkspaceID, userID, "", "auth.invitation.accept", "invitation", invitation.ID, "success", nil)
	return invitation, nil
}

func (a *Auth) RevokeInvitation(ctx context.Context, workspaceID, actorUserID, invitationID string) error {
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return err
	}
	if err := a.store.RevokeInvitation(ctx, workspaceID, invitationID); err != nil {
		return err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.invitation.revoke", "invitation", invitationID, "success", nil)
	return nil
}

func validScope(scope string) bool {
	if scope == "*" {
		return true
	}
	switch scope {
	case models.ScopeFlipbooksRead, models.ScopeFlipbooksWrite, models.ScopeAnalyticsRead,
		models.ScopeLeadsRead, models.ScopeLeadsWrite, models.ScopeSalesRead,
		models.ScopeIntegrationsWrite, models.ScopeWorkspaceAdmin:
		return true
	}
	for _, prefix := range []string{"flipbooks:", "analytics:", "leads:", "sales:", "integrations:", "workspace:"} {
		if scope == prefix+"*" {
			return true
		}
	}
	return false
}

func scopeRole(scope string) models.Role {
	if scope == "*" || scope == "workspace:*" || scope == models.ScopeWorkspaceAdmin || scope == models.ScopeIntegrationsWrite || scope == "integrations:*" {
		return models.RoleAdmin
	}
	if strings.HasPrefix(scope, "flipbooks:") || scope == models.ScopeLeadsWrite || scope == "leads:*" {
		if scope == models.ScopeFlipbooksRead {
			return models.RoleReader
		}
		if scope == models.ScopeFlipbooksWrite || scope == "flipbooks:*" {
			return models.RoleEditor
		}
	}
	return models.RolePublisher
}

func (a *Auth) IssueAPIKey(ctx context.Context, workspaceID, actorUserID, name string, scopes []string, expiresAt *time.Time) (string, *models.APIKey, error) {
	membership, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin)
	if err != nil {
		return "", nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || len(scopes) == 0 {
		return "", nil, errors.New("API key name and scopes are required")
	}
	cleanScopes := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if !validScope(scope) || !membership.Role.AtLeast(scopeRole(scope)) {
			return "", nil, ErrForbidden
		}
		if _, ok := seen[scope]; !ok {
			seen[scope] = struct{}{}
			cleanScopes = append(cleanScopes, scope)
		}
	}
	if expiresAt != nil && !expiresAt.After(a.now()) {
		return "", nil, errors.New("API key expiry must be in the future")
	}
	secret, err := randomToken(a.random, 32)
	if err != nil {
		return "", nil, err
	}
	secret = "fbk_" + secret
	key := &models.APIKey{
		WorkspaceID: workspaceID, UserID: actorUserID, Name: name,
		Prefix: secret[:12], KeyHash: models.HashToken(secret), Scopes: cleanScopes, ExpiresAt: expiresAt,
	}
	if err := a.store.CreateAPIKey(ctx, key); err != nil {
		return "", nil, err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.api_key.create", "api_key", key.ID, "success", map[string]string{"scopes": strings.Join(cleanScopes, " ")})
	return secret, key, nil
}

func (a *Auth) RevokeAPIKey(ctx context.Context, workspaceID, actorUserID, keyID string) error {
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return err
	}
	if err := a.store.RevokeAPIKey(ctx, workspaceID, keyID); err != nil {
		return err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.api_key.revoke", "api_key", keyID, "success", nil)
	return nil
}
