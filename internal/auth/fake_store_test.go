package auth

import (
	"context"
	"errors"
	"html/template"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
)

type fakeStore struct {
	settings        map[string]string
	sessions        map[string]*models.Session
	users           map[string]*models.User
	workspaces      map[string]*models.Workspace
	memberships     map[string]*models.Membership
	invitations     map[string]*models.Invitation
	apiKeys         map[string]*models.APIKey
	audits          []*models.AuditEvent
	nextID          int
	readFailure     error
	writeFailure    error
	settingsFailure error
}

func newFakeStore() *fakeStore {
	store := &fakeStore{
		settings: make(map[string]string), sessions: make(map[string]*models.Session),
		users: make(map[string]*models.User), workspaces: make(map[string]*models.Workspace),
		memberships: make(map[string]*models.Membership), invitations: make(map[string]*models.Invitation),
		apiKeys: make(map[string]*models.APIKey),
	}
	now := time.Now().UTC()
	user := &models.User{ID: models.LegacyAdminUserID, Name: "Administrator", Locale: "en", CreatedAt: now, UpdatedAt: now}
	store.users[user.ID] = user
	store.workspaces[models.DefaultWorkspaceID] = &models.Workspace{ID: models.DefaultWorkspaceID, Name: "Default", OwnerID: user.ID}
	store.memberships[models.DefaultWorkspaceID+":"+user.ID] = &models.Membership{
		ID: models.DefaultWorkspaceID + ":" + user.ID, WorkspaceID: models.DefaultWorkspaceID, UserID: user.ID, Role: models.RoleOwner,
	}
	return store
}

func (f *fakeStore) id() string {
	f.nextID++
	return "test-id-" + time.Unix(int64(f.nextID), 0).Format("150405")
}

func (f *fakeStore) GetSetting(key string) (string, error) {
	if f.settingsFailure != nil {
		return "", f.settingsFailure
	}
	if f.readFailure != nil {
		return "", f.readFailure
	}
	value, ok := f.settings[key]
	if !ok {
		return "", database.ErrNotFound
	}
	return value, nil
}

func (f *fakeStore) SetSetting(key, value string) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	f.settings[key] = value
	return nil
}

func (f *fakeStore) CreateSessionRecord(_ context.Context, session *models.Session) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	copy := *session
	f.sessions[session.TokenHash] = &copy
	return nil
}

func (f *fakeStore) GetSessionByToken(_ context.Context, token string) (*models.Session, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	session, ok := f.sessions[models.HashToken(token)]
	if !ok {
		session, ok = f.sessions[token]
	}
	if !ok || !session.Active(time.Now().UTC()) {
		return nil, database.ErrNotFound
	}
	copy := *session
	return &copy, nil
}

func (f *fakeStore) TouchSession(_ context.Context, token string) error {
	if session, ok := f.sessions[models.HashToken(token)]; ok {
		now := time.Now().UTC()
		session.LastSeenAt = &now
	}
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, token string) error {
	now := time.Now().UTC()
	if session, ok := f.sessions[models.HashToken(token)]; ok {
		session.RevokedAt = &now
		return nil
	}
	if session, ok := f.sessions[token]; ok {
		session.RevokedAt = &now
	}
	return nil
}

func (f *fakeStore) RevokeUserSessions(_ context.Context, userID string) error {
	now := time.Now().UTC()
	for _, session := range f.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeStore) CreateUser(_ context.Context, user *models.User) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	user.Normalize()
	for _, existing := range f.users {
		if user.NormalizedEmail != "" && existing.NormalizedEmail == user.NormalizedEmail {
			return database.ErrConflict
		}
		if user.OIDCSubject != "" && existing.OIDCSubject == user.OIDCSubject && existing.OIDCIssuer == user.OIDCIssuer {
			return database.ErrConflict
		}
	}
	if user.ID == "" {
		user.ID = f.id()
	}
	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	copy := *user
	f.users[user.ID] = &copy
	return nil
}

func (f *fakeStore) GetUser(_ context.Context, userID string) (*models.User, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	user, ok := f.users[userID]
	if !ok {
		return nil, database.ErrNotFound
	}
	copy := *user
	return &copy, nil
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (*models.User, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	for _, user := range f.users {
		if user.NormalizedEmail == normalized {
			copy := *user
			return &copy, nil
		}
	}
	return nil, database.ErrNotFound
}

func (f *fakeStore) GetUserByOIDC(_ context.Context, issuer, subject string) (*models.User, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	for _, user := range f.users {
		if user.OIDCIssuer == issuer && user.OIDCSubject == subject {
			copy := *user
			return &copy, nil
		}
	}
	return nil, database.ErrNotFound
}

func (f *fakeStore) UpdateUserProfile(_ context.Context, userID, name, locale, timeZone string) error {
	user, ok := f.users[userID]
	if !ok {
		return database.ErrNotFound
	}
	user.Name, user.Locale, user.TimeZone = name, locale, timeZone
	user.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeStore) SetUserPassword(_ context.Context, userID, passwordHash string) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	user, ok := f.users[userID]
	if !ok {
		return database.ErrNotFound
	}
	now := time.Now().UTC()
	user.PasswordHash = passwordHash
	user.PasswordChangedAt = &now
	user.UpdatedAt = now
	return f.RevokeUserSessions(context.Background(), userID)
}

func (f *fakeStore) GetWorkspace(_ context.Context, workspaceID string) (*models.Workspace, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	workspace, ok := f.workspaces[workspaceID]
	if !ok {
		return nil, database.ErrNotFound
	}
	copy := *workspace
	return &copy, nil
}

func (f *fakeStore) ListWorkspacesForUser(_ context.Context, userID string) ([]*models.Workspace, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	result := make([]*models.Workspace, 0)
	for _, membership := range f.memberships {
		if membership.UserID == userID {
			if workspace, ok := f.workspaces[membership.WorkspaceID]; ok {
				copy := *workspace
				result = append(result, &copy)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (f *fakeStore) GetMembership(_ context.Context, workspaceID, userID string) (*models.Membership, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	membership, ok := f.memberships[workspaceID+":"+userID]
	if !ok {
		return nil, database.ErrNotFound
	}
	copy := *membership
	return &copy, nil
}

func (f *fakeStore) UpsertMembership(_ context.Context, membership *models.Membership) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	if !membership.Role.Valid() {
		return errors.New("invalid role")
	}
	membership.ID = membership.WorkspaceID + ":" + membership.UserID
	copy := *membership
	f.memberships[membership.ID] = &copy
	return nil
}

func (f *fakeStore) CreateInvitation(_ context.Context, invitation *models.Invitation) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	if invitation.ID == "" {
		invitation.ID = f.id()
	}
	invitation.NormalizedEmail = strings.ToLower(strings.TrimSpace(invitation.Email))
	if invitation.CreatedAt.IsZero() {
		invitation.CreatedAt = time.Now().UTC()
	}
	copy := *invitation
	f.invitations[invitation.ID] = &copy
	return nil
}

func (f *fakeStore) GetInvitationByToken(_ context.Context, token string) (*models.Invitation, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	for _, invitation := range f.invitations {
		if invitation.TokenHash == models.HashToken(token) && invitation.AcceptedAt == nil && invitation.RevokedAt == nil && time.Now().UTC().Before(invitation.ExpiresAt) {
			copy := *invitation
			return &copy, nil
		}
	}
	return nil, database.ErrNotFound
}

func (f *fakeStore) AcceptInvitation(ctx context.Context, token, userID string) error {
	invitation, err := f.GetInvitationByToken(ctx, token)
	if err != nil {
		return err
	}
	user, err := f.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if user.NormalizedEmail != invitation.NormalizedEmail {
		return database.ErrConflict
	}
	if err := f.UpsertMembership(ctx, &models.Membership{WorkspaceID: invitation.WorkspaceID, UserID: userID, Role: invitation.Role, InvitedBy: invitation.InvitedBy}); err != nil {
		return err
	}
	now := time.Now().UTC()
	f.invitations[invitation.ID].AcceptedAt = &now
	return nil
}

func (f *fakeStore) RevokeInvitation(_ context.Context, workspaceID, invitationID string) error {
	invitation, ok := f.invitations[invitationID]
	if !ok || invitation.WorkspaceID != workspaceID {
		return database.ErrNotFound
	}
	now := time.Now().UTC()
	invitation.RevokedAt = &now
	return nil
}

func (f *fakeStore) CreateAPIKey(_ context.Context, key *models.APIKey) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	if key.ID == "" {
		key.ID = f.id()
	}
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	copy := *key
	f.apiKeys[key.ID] = &copy
	return nil
}

func (f *fakeStore) AuthenticateAPIKey(_ context.Context, secret, requiredScope string) (*models.APIKey, error) {
	if f.readFailure != nil {
		return nil, f.readFailure
	}
	for _, key := range f.apiKeys {
		if key.KeyHash == models.HashToken(secret) {
			if !key.Active(time.Now().UTC()) {
				return nil, database.ErrNotFound
			}
			if requiredScope != "" && !key.HasScope(requiredScope) {
				return nil, database.ErrForbidden
			}
			now := time.Now().UTC()
			key.LastUsedAt = &now
			copy := *key
			return &copy, nil
		}
	}
	return nil, database.ErrNotFound
}

func (f *fakeStore) RevokeAPIKey(_ context.Context, workspaceID, keyID string) error {
	key, ok := f.apiKeys[keyID]
	if !ok || key.WorkspaceID != workspaceID {
		return database.ErrNotFound
	}
	now := time.Now().UTC()
	key.RevokedAt = &now
	return nil
}

func (f *fakeStore) RecordAuditEvent(_ context.Context, event *models.AuditEvent) error {
	if f.writeFailure != nil {
		return f.writeFailure
	}
	if event.ID == "" {
		event.ID = f.id()
	}
	copy := *event
	f.audits = append(f.audits, &copy)
	return nil
}

func testAuth(t *testing.T, store *fakeStore, opts Options) *Auth {
	t.Helper()
	tmpl, err := template.New("").ParseFiles("../../web/templates/login.html")
	if err != nil {
		t.Fatal(err)
	}
	if opts.SessionSecret == "" {
		opts.SessionSecret = strings.Repeat("test-secret-", 8)
	}
	if opts.LoginAttemptsPerMinute == 0 {
		opts.LoginAttemptsPerMinute = 1000
	}
	auth, err := NewWithOptions(store, tmpl, opts)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func addUser(t *testing.T, store *fakeStore, email, password string, role models.Role) *models.User {
	t.Helper()
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user := &models.User{Email: email, Name: email, PasswordHash: hash}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMembership(context.Background(), &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: user.ID, Role: role}); err != nil {
		t.Fatal(err)
	}
	return user
}
