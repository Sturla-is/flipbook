package database

import (
	"context"
	"errors"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (d *DB) CreateSessionRecord(ctx context.Context, session *models.Session) error {
	if session.TokenHash == "" || session.ExpiresAt.IsZero() {
		return errors.New("session token hash and expiry are required")
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	_, err := d.sessions.InsertOne(ctx, session)
	return err
}

func (d *DB) GetSessionByToken(ctx context.Context, token string) (*models.Session, error) {
	if token == "" {
		return nil, mongo.ErrNoDocuments
	}
	var session models.Session
	filter := bson.M{"_id": models.HashToken(token), "expires_at": bson.M{"$gt": time.Now().UTC()}, "revoked_at": nil}
	err := d.sessions.FindOne(ctx, filter).Decode(&session)
	if err == mongo.ErrNoDocuments {
		// Legacy sessions used the raw token as their _id. They remain valid
		// until expiry, while all newly created sessions can use hashed tokens.
		filter["_id"] = token
		err = d.sessions.FindOne(ctx, filter).Decode(&session)
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (d *DB) TouchSession(ctx context.Context, token string) error {
	now := time.Now().UTC()
	_, err := d.sessions.UpdateOne(ctx, bson.M{"_id": models.HashToken(token)}, bson.M{"$set": bson.M{"last_seen_at": now}})
	return err
}

func (d *DB) RevokeSession(ctx context.Context, token string) error {
	now := time.Now().UTC()
	result, err := d.sessions.UpdateOne(ctx, bson.M{"_id": models.HashToken(token)}, bson.M{"$set": bson.M{"revoked_at": now}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		_, err = d.sessions.UpdateOne(ctx, bson.M{"_id": token}, bson.M{"$set": bson.M{"revoked_at": now}})
	}
	return err
}

func (d *DB) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := d.sessions.UpdateMany(ctx, bson.M{"user_id": userID, "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": time.Now().UTC()}})
	return err
}

func (d *DB) CreateAPIKey(ctx context.Context, key *models.APIKey) error {
	if key.WorkspaceID == "" || key.UserID == "" || key.KeyHash == "" || len(key.Scopes) == 0 {
		return errors.New("API key workspace, user, hash, and scopes are required")
	}
	if key.ID == "" {
		key.ID = newID()
	}
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	_, err := d.apiKeys.InsertOne(ctx, key)
	return err
}

func (d *DB) AuthenticateAPIKey(ctx context.Context, secret, requiredScope string) (*models.APIKey, error) {
	if secret == "" {
		return nil, mongo.ErrNoDocuments
	}
	var key models.APIKey
	filter := bson.M{
		"key_hash": models.HashToken(secret), "revoked_at": nil,
		"$or": bson.A{bson.M{"expires_at": nil}, bson.M{"expires_at": bson.M{"$gt": time.Now().UTC()}}},
	}
	if err := d.apiKeys.FindOne(ctx, filter).Decode(&key); err != nil {
		return nil, err
	}
	if requiredScope != "" && !key.HasScope(requiredScope) {
		return nil, ErrForbidden
	}
	_, _ = d.apiKeys.UpdateByID(ctx, key.ID, bson.M{"$set": bson.M{"last_used_at": time.Now().UTC()}})
	return &key, nil
}

func (d *DB) ListAPIKeys(ctx context.Context, workspaceID string) ([]*models.APIKey, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	return decodeCursor[models.APIKey](ctx, d.apiKeys, bson.M{"workspace_id": workspaceID}, opts)
}

func (d *DB) RevokeAPIKey(ctx context.Context, workspaceID, keyID string) error {
	result, err := d.apiKeys.UpdateOne(ctx, bson.M{"_id": keyID, "workspace_id": workspaceID, "revoked_at": nil}, bson.M{"$set": bson.M{"revoked_at": time.Now().UTC()}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

type AuditFilter struct {
	WorkspaceID  string
	ResourceType string
	ResourceID   string
	Action       string
	From         time.Time
	To           time.Time
	Limit        int64
}

func (d *DB) RecordAuditEvent(ctx context.Context, event *models.AuditEvent) error {
	if event.WorkspaceID == "" || event.Action == "" {
		return errors.New("audit workspace and action are required")
	}
	if event.ID == "" {
		event.ID = newID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	_, err := d.auditEvents.InsertOne(ctx, event)
	return err
}

func (d *DB) ListAuditEvents(ctx context.Context, filter AuditFilter) ([]*models.AuditEvent, error) {
	query := bson.M{"workspace_id": filter.WorkspaceID}
	if filter.ResourceType != "" {
		query["resource_type"] = filter.ResourceType
	}
	if filter.ResourceID != "" {
		query["resource_id"] = filter.ResourceID
	}
	if filter.Action != "" {
		query["action"] = filter.Action
	}
	if !filter.From.IsZero() || !filter.To.IsZero() {
		rangeQuery := bson.M{}
		if !filter.From.IsZero() {
			rangeQuery["$gte"] = filter.From
		}
		if !filter.To.IsZero() {
			rangeQuery["$lt"] = filter.To
		}
		query["created_at"] = rangeQuery
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("audit limit must be between 1 and 1000")
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	return decodeCursor[models.AuditEvent](ctx, d.auditEvents, query, opts)
}
