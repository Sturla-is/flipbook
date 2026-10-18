package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (d *DB) CreateUser(ctx context.Context, user *models.User) error {
	user.Normalize()
	if user.NormalizedEmail == "" && (user.OIDCSubject == "" || user.OIDCIssuer == "") {
		return errors.New("user email or OIDC identity is required")
	}
	if user.ID == "" {
		user.ID = newID()
	}
	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	_, err := d.users.InsertOne(ctx, user)
	return err
}

func (d *DB) GetUser(ctx context.Context, userID string) (*models.User, error) {
	var user models.User
	if err := d.users.FindOne(ctx, bson.M{"_id": userID}).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	normalized := strings.ToLower(strings.TrimSpace(email))
	if err := d.users.FindOne(ctx, bson.M{"normalized_email": normalized}).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) GetUserByOIDC(ctx context.Context, issuer, subject string) (*models.User, error) {
	var user models.User
	if err := d.users.FindOne(ctx, bson.M{"oidc_issuer": issuer, "oidc_subject": subject}).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (d *DB) UpdateUserProfile(ctx context.Context, userID, name, locale, timeZone string) error {
	_, err := d.users.UpdateByID(ctx, userID, bson.M{"$set": bson.M{
		"name": name, "locale": locale, "time_zone": timeZone, "updated_at": time.Now().UTC(),
	}})
	return err
}

func (d *DB) SetUserPassword(ctx context.Context, userID, passwordHash string) error {
	if passwordHash == "" {
		return errors.New("password hash is required")
	}
	now := time.Now().UTC()
	result, err := d.users.UpdateByID(ctx, userID, bson.M{"$set": bson.M{
		"password_hash": passwordHash, "password_changed_at": now, "updated_at": now,
	}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	_, err = d.sessions.UpdateMany(ctx, bson.M{"user_id": userID, "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": now}})
	return err
}

func (d *DB) SetUserDisabled(ctx context.Context, userID string, disabled bool) error {
	now := time.Now().UTC()
	set := bson.M{"updated_at": now}
	update := bson.M{"$set": set, "$unset": bson.M{"disabled_at": ""}}
	if disabled {
		set["disabled_at"] = now
		delete(update, "$unset")
	}
	result, err := d.users.UpdateByID(ctx, userID, update)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	if disabled {
		_, err = d.sessions.UpdateMany(ctx, bson.M{"user_id": userID, "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": now}})
	}
	return err
}

func (d *DB) CreateWorkspace(ctx context.Context, workspace *models.Workspace, ownerID string) error {
	if workspace.Name == "" || ownerID == "" {
		return errors.New("workspace name and owner are required")
	}
	if _, err := d.GetUser(ctx, ownerID); err != nil {
		return err
	}
	if workspace.ID == "" {
		workspace.ID = newID()
	}
	if workspace.Slug == "" {
		workspace.Slug = models.NormalizeSlug(workspace.Name)
	}
	workspace.Slug = models.NormalizeSlug(workspace.Slug)
	if workspace.Slug == "" {
		return errors.New("workspace slug is required")
	}
	workspace.OwnerID = ownerID
	workspace.ApplyDefaults()
	now := time.Now().UTC()
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = now
	}
	workspace.UpdatedAt = now
	if _, err := d.workspaces.InsertOne(ctx, workspace); err != nil {
		return err
	}
	membership := models.Membership{
		ID: workspace.ID + ":" + ownerID, WorkspaceID: workspace.ID, UserID: ownerID,
		Role: models.RoleOwner, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := d.memberships.InsertOne(ctx, membership); err != nil {
		_, _ = d.workspaces.DeleteOne(ctx, bson.M{"_id": workspace.ID})
		return err
	}
	return nil
}

func (d *DB) GetWorkspace(ctx context.Context, workspaceID string) (*models.Workspace, error) {
	var workspace models.Workspace
	if err := d.workspaces.FindOne(ctx, bson.M{"_id": workspaceID}).Decode(&workspace); err != nil {
		return nil, err
	}
	return &workspace, nil
}

func (d *DB) GetWorkspaceBySlug(ctx context.Context, slug string) (*models.Workspace, error) {
	var workspace models.Workspace
	if err := d.workspaces.FindOne(ctx, bson.M{"slug": models.NormalizeSlug(slug)}).Decode(&workspace); err != nil {
		return nil, err
	}
	return &workspace, nil
}

func (d *DB) GetWorkspaceByDomain(ctx context.Context, host string) (*models.Workspace, error) {
	var workspace models.Workspace
	host = strings.ToLower(strings.TrimSpace(host))
	if err := d.workspaces.FindOne(ctx, bson.M{"settings.custom_domains": bson.M{"$elemMatch": bson.M{"host": host, "verified": true}}}).Decode(&workspace); err != nil {
		return nil, err
	}
	return &workspace, nil
}

func (d *DB) UpdateWorkspaceSettings(ctx context.Context, workspace *models.Workspace) error {
	if workspace.ID == "" {
		return errors.New("workspace id is required")
	}
	workspace.ApplyDefaults()
	result, err := d.workspaces.UpdateByID(ctx, workspace.ID, bson.M{"$set": bson.M{
		"name": workspace.Name, "settings": workspace.Settings, "updated_at": time.Now().UTC(),
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) ListWorkspacesForUser(ctx context.Context, userID string) ([]*models.Workspace, error) {
	memberships, err := decodeCursor[models.Membership](ctx, d.memberships, bson.M{"user_id": userID})
	if err != nil {
		return nil, err
	}
	ids := make(bson.A, 0, len(memberships))
	for _, membership := range memberships {
		ids = append(ids, membership.WorkspaceID)
	}
	opts := options.Find().SetSort(bson.D{{Key: "name", Value: 1}})
	return decodeCursor[models.Workspace](ctx, d.workspaces, bson.M{"_id": bson.M{"$in": ids}}, opts)
}

func (d *DB) UpsertMembership(ctx context.Context, membership *models.Membership) error {
	if !membership.Role.Valid() {
		return fmt.Errorf("invalid role %q", membership.Role)
	}
	if membership.WorkspaceID == "" || membership.UserID == "" {
		return errors.New("workspace and user are required")
	}
	if membership.Role != models.RoleOwner {
		var existing models.Membership
		err := d.memberships.FindOne(ctx, bson.M{"workspace_id": membership.WorkspaceID, "user_id": membership.UserID}).Decode(&existing)
		if err == nil && existing.Role == models.RoleOwner {
			owners, countErr := d.memberships.CountDocuments(ctx, bson.M{"workspace_id": membership.WorkspaceID, "role": models.RoleOwner})
			if countErr != nil {
				return countErr
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		} else if err != nil && err != mongo.ErrNoDocuments {
			return err
		}
	}
	now := time.Now().UTC()
	membership.ID = membership.WorkspaceID + ":" + membership.UserID
	if membership.CreatedAt.IsZero() {
		membership.CreatedAt = now
	}
	membership.UpdatedAt = now
	_, err := d.memberships.ReplaceOne(ctx, bson.M{"_id": membership.ID}, membership, options.Replace().SetUpsert(true))
	return err
}

func (d *DB) GetMembership(ctx context.Context, workspaceID, userID string) (*models.Membership, error) {
	var membership models.Membership
	if err := d.memberships.FindOne(ctx, bson.M{"workspace_id": workspaceID, "user_id": userID}).Decode(&membership); err != nil {
		return nil, err
	}
	return &membership, nil
}

func (d *DB) ListMemberships(ctx context.Context, workspaceID string) ([]*models.Membership, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})
	return decodeCursor[models.Membership](ctx, d.memberships, bson.M{"workspace_id": workspaceID}, opts)
}

func (d *DB) RemoveMembership(ctx context.Context, workspaceID, userID string) error {
	membership, err := d.GetMembership(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	if membership.Role == models.RoleOwner {
		owners, err := d.memberships.CountDocuments(ctx, bson.M{"workspace_id": workspaceID, "role": models.RoleOwner})
		if err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	_, err = d.memberships.DeleteOne(ctx, bson.M{"_id": membership.ID})
	return err
}

func (d *DB) CreateInvitation(ctx context.Context, invitation *models.Invitation) error {
	if !invitation.Role.Valid() || invitation.Role == models.RoleOwner {
		return errors.New("invitation role must be admin, publisher, editor, or reader")
	}
	invitation.NormalizedEmail = strings.ToLower(strings.TrimSpace(invitation.Email))
	if invitation.NormalizedEmail == "" || invitation.TokenHash == "" || invitation.ExpiresAt.IsZero() {
		return errors.New("invitation email, token hash, and expiry are required")
	}
	if invitation.ID == "" {
		invitation.ID = newID()
	}
	if invitation.CreatedAt.IsZero() {
		invitation.CreatedAt = time.Now().UTC()
	}
	_, err := d.invitations.InsertOne(ctx, invitation)
	return err
}

func (d *DB) GetInvitationByToken(ctx context.Context, token string) (*models.Invitation, error) {
	var invitation models.Invitation
	filter := bson.M{
		"token_hash": models.HashToken(token), "accepted_at": nil, "revoked_at": nil,
		"expires_at": bson.M{"$gt": time.Now().UTC()},
	}
	if err := d.invitations.FindOne(ctx, filter).Decode(&invitation); err != nil {
		return nil, err
	}
	return &invitation, nil
}

func (d *DB) AcceptInvitation(ctx context.Context, token, userID string) error {
	invitation, err := d.GetInvitationByToken(ctx, token)
	if err != nil {
		return err
	}
	user, err := d.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if user.NormalizedEmail != invitation.NormalizedEmail {
		return ErrConflict
	}
	if err := d.UpsertMembership(ctx, &models.Membership{WorkspaceID: invitation.WorkspaceID, UserID: userID, Role: invitation.Role, InvitedBy: invitation.InvitedBy}); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = d.invitations.UpdateByID(ctx, invitation.ID, bson.M{"$set": bson.M{"accepted_at": now}})
	return err
}

func (d *DB) RevokeInvitation(ctx context.Context, workspaceID, invitationID string) error {
	_, err := d.invitations.UpdateOne(ctx, bson.M{"_id": invitationID, "workspace_id": workspaceID}, bson.M{"$set": bson.M{"revoked_at": time.Now().UTC()}})
	return err
}
