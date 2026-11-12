package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
)

func TestCreateLocalUserProvisionsAccountAndMembership(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	user, err := a.CreateLocalUser(context.Background(), models.DefaultWorkspaceID, models.LegacyAdminUserID, "Jane Admin", "jane@example.com", testPassword, models.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if user.ID == "" || user.Name != "Jane Admin" || user.NormalizedEmail != "jane@example.com" || user.PasswordHash == "" {
		t.Fatalf("user was not provisioned correctly: %+v", user)
	}
	if !passwordMatches(user.PasswordHash, testPassword) {
		t.Fatal("stored password hash does not match")
	}
	membership, err := store.GetMembership(context.Background(), models.DefaultWorkspaceID, user.ID)
	if err != nil || membership.Role != models.RoleAdmin || membership.InvitedBy != models.LegacyAdminUserID {
		t.Fatalf("membership was not provisioned correctly: %+v err=%v", membership, err)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "auth.user.create" {
		t.Fatalf("expected one user.create audit: %+v", store.audits)
	}
}

func TestCreateLocalUserRejectsBadInputs(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	ctx := context.Background()

	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, "Jane", "jane@example.com", testPassword, models.RoleOwner); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner role was accepted: %v", err)
	}
	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, "Jane", "not-an-email", testPassword, models.RoleAdmin); err == nil {
		t.Fatal("invalid email was accepted")
	}
	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, "", "jane@example.com", testPassword, models.RoleAdmin); err == nil {
		t.Fatal("empty name was accepted")
	}
	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, "Jane", "jane@example.com", "short", models.RoleAdmin); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password was not rejected: %v", err)
	}

	// A non-admin actor cannot create accounts.
	reader := addUser(t, store, "reader@example.com", testPassword, models.RoleReader)
	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, reader.ID, "Jane", "jane@example.com", testPassword, models.RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader was allowed to create an account: %v", err)
	}

	// A second account with the same email is rejected.
	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, "Jane", "jane@example.com", testPassword, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateLocalUser(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, "Jane Two", "Jane@Example.com", testPassword, models.RoleAdmin); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("duplicate email was not rejected: %v", err)
	}
}

func TestSetUserPasswordByAdminResetsPasswordAndRevokesSessions(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	ctx := context.Background()
	target := addUser(t, store, "member@example.com", testPassword, models.RoleEditor)
	sessionToken := sessionForUser(t, a, target, models.DefaultWorkspaceID)

	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, target.ID, "new-password-123"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetUser(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !passwordMatches(updated.PasswordHash, "new-password-123") {
		t.Fatal("password was not updated")
	}
	if _, err := a.principalFromRequest(authenticatedRequest(sessionToken, "/admin")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old session remained valid after password reset: %v", err)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "auth.password.admin_set" {
		t.Fatalf("expected one password admin_set audit: %+v", store.audits)
	}

	// A reader actor cannot reset passwords.
	reader := addUser(t, store, "reader@example.com", testPassword, models.RoleReader)
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, reader.ID, target.ID, "another-password-123"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader was allowed to reset a password: %v", err)
	}

	// Only an owner may reset the owner's password.
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, reader.ID, models.LegacyAdminUserID, "another-password-123"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin was allowed to reset the owner's password: %v", err)
	}
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, models.LegacyAdminUserID, "owner-password-123"); err != nil {
		t.Fatalf("owner could not reset own password: %v", err)
	}
}

func TestSetLegacyAdminPasswordUpdatesBothRepresentations(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	if err := a.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	legacyHash := store.settings[settingPwdHash]
	sessionToken := sessionForUser(t, a, store.users[models.LegacyAdminUserID], models.DefaultWorkspaceID)

	if err := a.SetLegacyAdminPassword(context.Background(), models.DefaultWorkspaceID, models.LegacyAdminUserID, "rotated-password-123"); err != nil {
		t.Fatal(err)
	}
	if store.settings[settingPwdHash] == legacyHash || store.settings[settingPwdHash] == "" {
		t.Fatal("legacy setting was not rotated")
	}
	if !passwordMatches(store.users[models.LegacyAdminUserID].PasswordHash, "rotated-password-123") {
		t.Fatal("legacy admin user hash was not updated")
	}
	if _, err := a.principalFromRequest(authenticatedRequest(sessionToken, "/admin")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy session remained valid after rotation: %v", err)
	}

	// A non-admin member cannot rotate the legacy admin password.
	reader := addUser(t, store, "reader@example.com", testPassword, models.RoleReader)
	if err := a.SetLegacyAdminPassword(context.Background(), models.DefaultWorkspaceID, reader.ID, "hijack-password-123"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader rotated the legacy admin password: %v", err)
	}
}

func TestRuntimeSessionTTLOverride(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{SessionTTL: 7 * 24 * time.Hour})
	user := store.users[models.LegacyAdminUserID]

	store.settings[SettingSessionTTLSeconds] = strconv.Itoa(48 * 3600)
	recorder := httptest.NewRecorder()
	principal, err := a.createSessionForUser(context.Background(), recorder, user, models.DefaultWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	want := principal.Session.CreatedAt.Add(48 * time.Hour)
	if !principal.Session.ExpiresAt.Equal(want) {
		t.Fatalf("session TTL override ignored: got %v want %v", principal.Session.ExpiresAt, want)
	}

	// Invalid overrides fall back to the configured TTL.
	store.settings[SettingSessionTTLSeconds] = "not-a-number"
	recorder = httptest.NewRecorder()
	principal, err = a.createSessionForUser(context.Background(), recorder, user, models.DefaultWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Session.ExpiresAt.Equal(principal.Session.CreatedAt.Add(7 * 24 * time.Hour)) {
		t.Fatalf("invalid override did not fall back: %v", principal.Session.ExpiresAt)
	}
}

func TestRuntimeLoginAttemptLimitOverride(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{LoginAttemptsPerMinute: 1000})
	store.settings[SettingLoginAttemptsPerMinute] = "2"

	allowed := 0
	for i := 0; i < 3; i++ {
		ok, _ := a.throttleAllow("account:test@example.com")
		if ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("runtime login limit not applied: allowed %d attempts", allowed)
	}

	store.settings[SettingLoginAttemptsPerMinute] = "not-a-number"
	ok, _ := a.throttleAllow("account:other@example.com")
	if !ok {
		t.Fatal("invalid override broke the throttle")
	}
}

func TestRuntimeSettingFailuresFallBackQuietly(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{SessionTTL: 24 * time.Hour, LoginAttemptsPerMinute: 10})
	store.settingsFailure = errors.New("settings unavailable")

	recorder := httptest.NewRecorder()
	principal, err := a.createSessionForUser(context.Background(), recorder, store.users[models.LegacyAdminUserID], models.DefaultWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Session.ExpiresAt.Equal(principal.Session.CreatedAt.Add(24 * time.Hour)) {
		t.Fatalf("store failure changed session TTL: %v", principal.Session.ExpiresAt)
	}
	if ok, _ := a.throttleAllow("account:fallback@example.com"); !ok {
		t.Fatal("store failure blocked the throttle")
	}
	store.settingsFailure = nil
}

func TestCreateLocalUserRequiresConfiguredAuth(t *testing.T) {
	store := newFakeStore()
	var a *Auth
	if _, err := a.CreateLocalUser(context.Background(), models.DefaultWorkspaceID, "actor", "Jane", "jane@example.com", testPassword, models.RoleAdmin); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil auth did not fail closed: %v", err)
	}
	if err := a.SetUserPasswordByAdmin(context.Background(), models.DefaultWorkspaceID, "actor", "target", testPassword); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil auth password reset did not fail closed: %v", err)
	}
	if err := a.SetLegacyAdminPassword(context.Background(), models.DefaultWorkspaceID, "actor", testPassword); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil auth legacy rotation did not fail closed: %v", err)
	}
	_ = store
}

func TestAdminSettingsAuditMessagesAreSanitized(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	email := strings.ToUpper("Audit@Example.com ")
	user, err := a.CreateLocalUser(context.Background(), models.DefaultWorkspaceID, models.LegacyAdminUserID, "Audit User", email, testPassword, models.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if user.NormalizedEmail != "audit@example.com" {
		t.Fatalf("email was not normalized: %q", user.NormalizedEmail)
	}
	if len(store.audits) != 1 || store.audits[0].Metadata["role"] != "admin" {
		t.Fatalf("unexpected audit payload: %+v", store.audits)
	}
}

func addMembership(t *testing.T, store *fakeStore, workspaceID, userID string, role models.Role) {
	t.Helper()
	if _, ok := store.workspaces[workspaceID]; !ok {
		store.workspaces[workspaceID] = &models.Workspace{ID: workspaceID, Name: workspaceID, OwnerID: userID}
	}
	if err := store.UpsertMembership(context.Background(), &models.Membership{WorkspaceID: workspaceID, UserID: userID, Role: role}); err != nil {
		t.Fatal(err)
	}
}

func TestSetUserPasswordByAdminLetsAdminsResetPeersButNotOwners(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	ctx := context.Background()
	admin := addUser(t, store, "admin@example.com", testPassword, models.RoleAdmin)
	peer := addUser(t, store, "peer@example.com", testPassword, models.RoleAdmin)

	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, admin.ID, peer.ID, "peer-password-123"); err != nil {
		t.Fatalf("admin could not reset a peer admin: %v", err)
	}
	if !passwordMatches(store.users[peer.ID].PasswordHash, "peer-password-123") {
		t.Fatal("peer password was not updated")
	}
	err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, admin.ID, models.LegacyAdminUserID, "takeover-password-123")
	if !errors.Is(err, ErrPasswordResetOwner) || !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin reset the owner's password: %v", err)
	}
	if err := a.SetLegacyAdminPassword(ctx, models.DefaultWorkspaceID, admin.ID, "takeover-password-123"); !errors.Is(err, ErrPasswordResetOwner) {
		t.Fatalf("admin rotated the owner's primary admin password: %v", err)
	}
	if err := a.CheckLegacyAdminPasswordReset(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID); err != nil {
		t.Fatalf("owner could not rotate own primary password: %v", err)
	}
}

func TestSetUserPasswordByAdminRequiresAuthorityInEveryWorkspace(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	ctx := context.Background()
	admin := addUser(t, store, "admin@example.com", testPassword, models.RoleAdmin)

	// Owns another workspace: an admin here must not be able to take it over.
	foreignOwner := addUser(t, store, "owner-b@example.com", testPassword, models.RoleReader)
	addMembership(t, store, "workspace-b", foreignOwner.ID, models.RoleOwner)
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, admin.ID, foreignOwner.ID, "takeover-password-123"); !errors.Is(err, ErrPasswordResetOtherWorkspace) {
		t.Fatalf("admin reset the owner of another workspace: %v", err)
	}
	if !passwordMatches(store.users[foreignOwner.ID].PasswordHash, testPassword) {
		t.Fatal("refused reset still changed the password")
	}

	// A member elsewhere, in a workspace the actor does not belong to.
	outsider := addUser(t, store, "outsider@example.com", testPassword, models.RoleEditor)
	addMembership(t, store, "workspace-c", outsider.ID, models.RoleReader)
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, admin.ID, outsider.ID, "takeover-password-123"); !errors.Is(err, ErrPasswordResetOtherWorkspace) {
		t.Fatalf("admin reset a member of a workspace they do not belong to: %v", err)
	}

	// Allowed once the actor administers every workspace the target is in.
	addMembership(t, store, "workspace-c", admin.ID, models.RoleAdmin)
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, admin.ID, outsider.ID, "shared-password-123"); err != nil {
		t.Fatalf("admin of both workspaces could not reset: %v", err)
	}
	if !passwordMatches(store.users[outsider.ID].PasswordHash, "shared-password-123") {
		t.Fatal("password was not updated")
	}
}

func TestSetUserPasswordByAdminRefusesSSOAccountsAndNonMembers(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	ctx := context.Background()
	sso := addUser(t, store, "sso@example.com", testPassword, models.RoleEditor)
	store.users[sso.ID].OIDCIssuer = "https://idp.example.com"
	store.users[sso.ID].OIDCSubject = "subject-1"
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, sso.ID, "local-password-123"); !errors.Is(err, ErrPasswordManagedBySSO) {
		t.Fatalf("single-sign-on account got a local password: %v", err)
	}

	stranger := addUser(t, store, "stranger@example.com", testPassword, models.RoleEditor)
	delete(store.memberships, models.DefaultWorkspaceID+":"+stranger.ID)
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, stranger.ID, "stranger-password-123"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("password set for a user outside the workspace: %v", err)
	}
}

func TestPasswordChangesOnLegacyAdminUpdateBothRepresentations(t *testing.T) {
	store := newFakeStore()
	a := testAuth(t, store, Options{})
	ctx := context.Background()
	if err := a.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}

	// Reset through the member path, as the Settings and Team tables do.
	if err := a.SetUserPasswordByAdmin(ctx, models.DefaultWorkspaceID, models.LegacyAdminUserID, models.LegacyAdminUserID, "member-path-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.legacyPasswordUser(ctx, "member-path-123", false); err != nil {
		t.Fatalf("new primary admin password is rejected at sign-in: %v", err)
	}
	if _, err := a.legacyPasswordUser(ctx, testPassword, false); err == nil {
		t.Fatal("old primary admin password still signs in")
	}

	// Self-service change with the current password.
	if err := a.ChangePassword(ctx, models.LegacyAdminUserID, "member-path-123", "self-service-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.legacyPasswordUser(ctx, "self-service-123", false); err != nil {
		t.Fatalf("changed primary admin password is rejected at sign-in: %v", err)
	}
	if err := a.ChangePassword(ctx, models.LegacyAdminUserID, "wrong-password-123", "another-password-123"); !errors.Is(err, errAuthenticationFailed) {
		t.Fatalf("wrong current password was accepted: %v", err)
	}
}
