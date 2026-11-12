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
	if userID == models.LegacyAdminUserID {
		// The primary "admin" login verifies against the legacy setting first.
		if _, err := a.legacyPasswordUser(ctx, currentPassword, false); err != nil {
			return err
		}
	} else {
		user, err := a.store.GetUser(ctx, userID)
		if err != nil {
			return err
		}
		if !user.Active() || !passwordMatches(user.PasswordHash, currentPassword) {
			return errAuthenticationFailed
		}
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := a.storeUserHash(ctx, userID, hash); err != nil {
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

// accountAuthority reports whether actorUserID may act on targetUserID's
// account as a whole: set its password without knowing the current one, or
// disable/enable it. Both take effect in every workspace the target belongs
// to, so the actor needs authority over the target in each of them: at least
// admin there, and at least the target's role there (an owner where the
// target is an owner). Checking only the current workspace would let the
// admin of one workspace take over, or lock out, a user who owns another.
func (a *Auth) accountAuthority(ctx context.Context, workspaceID, actorUserID, targetUserID string) error {
	if actorUserID == targetUserID {
		return nil
	}
	workspaces, err := a.store.ListWorkspacesForUser(ctx, targetUserID)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		targetMembership, err := a.store.GetMembership(ctx, workspace.ID, targetUserID)
		if errors.Is(err, database.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		actorMembership, err := a.store.GetMembership(ctx, workspace.ID, actorUserID)
		if err != nil && !errors.Is(err, database.ErrNotFound) {
			return err
		}
		if err == nil && actorMembership.Role.AtLeast(models.RoleAdmin) && actorMembership.Role.AtLeast(targetMembership.Role) {
			continue
		}
		if workspace.ID == workspaceID && targetMembership.Role == models.RoleOwner {
			return ErrAccountActionOwner
		}
		return ErrAccountActionOtherWorkspace
	}
	return nil
}

// CheckAccountStatusChange reports whether an admin of workspaceID may
// disable or enable targetUserID, a member of that workspace, from the admin
// panel. The status lives on the user record, so it locks the target out of,
// or back into, every workspace they belong to; see accountAuthority. It
// returns nil when allowed; otherwise ErrForbidden (or one of the
// ErrAccountAction* errors wrapping it) or a store error. An actor can never
// change their own status: a disabled account could not sign in to undo it.
func (a *Auth) CheckAccountStatusChange(ctx context.Context, workspaceID, actorUserID, targetUserID string) error {
	if a == nil || a.initErr != nil {
		return ErrNotConfigured
	}
	if actorUserID == targetUserID {
		return ErrForbidden
	}
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return err
	}
	if _, err := a.store.GetMembership(ctx, workspaceID, targetUserID); err != nil {
		return err
	}
	return a.accountAuthority(ctx, workspaceID, actorUserID, targetUserID)
}

// CheckPasswordReset reports whether an admin of workspaceID may set the
// password of targetUserID, a member of that workspace, from the admin panel.
// It returns nil when allowed; otherwise ErrForbidden (or one of the
// ErrAccountAction* errors wrapping it), ErrPasswordManagedBySSO, or a store
// error.
func (a *Auth) CheckPasswordReset(ctx context.Context, workspaceID, actorUserID, targetUserID string) error {
	if a == nil || a.initErr != nil {
		return ErrNotConfigured
	}
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return err
	}
	if _, err := a.store.GetMembership(ctx, workspaceID, targetUserID); err != nil {
		return err
	}
	target, err := a.store.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}
	// A local password on a single-sign-on account would survive the user
	// being offboarded at the identity provider.
	if target.OIDCIssuer != "" || target.OIDCSubject != "" {
		return ErrPasswordManagedBySSO
	}
	return a.accountAuthority(ctx, workspaceID, actorUserID, targetUserID)
}

// SetUserPasswordByAdmin sets the local password of a workspace member from
// the admin panel and revokes every session of that user. See
// CheckPasswordReset for who may do this.
func (a *Auth) SetUserPasswordByAdmin(ctx context.Context, workspaceID, actorUserID, targetUserID, newPassword string) error {
	if err := a.CheckPasswordReset(ctx, workspaceID, actorUserID, targetUserID); err != nil {
		return err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := a.storeUserHash(ctx, targetUserID, hash); err != nil {
		return err
	}
	a.audit(ctx, workspaceID, actorUserID, "", "auth.password.admin_set", "user", targetUserID, "success", nil)
	return nil
}

// CheckLegacyAdminPasswordReset reports whether actorUserID may rotate the
// primary "admin" login password. The legacy admin owns the default
// workspace, so in practice only an owner there may, or the account itself.
func (a *Auth) CheckLegacyAdminPasswordReset(ctx context.Context, workspaceID, actorUserID string) error {
	if a == nil || a.initErr != nil {
		return ErrNotConfigured
	}
	if _, err := a.managementMembership(ctx, workspaceID, actorUserID, models.RoleAdmin); err != nil {
		return err
	}
	return a.accountAuthority(ctx, workspaceID, actorUserID, models.LegacyAdminUserID)
}

// SetLegacyAdminPassword rotates the primary legacy "admin" login password
// from the admin panel, replacing the CLI-only set-password flow for signed-in
// admins. It revokes every session of the legacy admin user.
func (a *Auth) SetLegacyAdminPassword(ctx context.Context, workspaceID, actorUserID, newPassword string) error {
	if err := a.CheckLegacyAdminPasswordReset(ctx, workspaceID, actorUserID); err != nil {
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
