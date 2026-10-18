package database

import (
	"context"
	"errors"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func (d *DB) CreateFlipbookAccessToken(ctx context.Context, workspaceID, flipbookID string, token *models.AccessToken) error {
	if token.ID == "" {
		token.ID = newID()
	}
	if token.TokenHash == "" {
		return errors.New("access token hash is required")
	}
	if token.CreatedAt.IsZero() {
		token.CreatedAt = time.Now().UTC()
	}
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{
		"_id": flipbookID, "workspace_id": workspaceID, "deleted_at": nil,
		"access_tokens.token_hash": bson.M{"$ne": token.TokenHash},
	}, bson.M{
		"$push": bson.M{"access_tokens": token},
		"$set":  bson.M{"updated_at": time.Now().UTC()},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrConflict
	}
	return nil
}

func (d *DB) GetFlipbookByAccessToken(ctx context.Context, rawToken string, now time.Time) (*models.Flipbook, error) {
	if rawToken == "" {
		return nil, mongo.ErrNoDocuments
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hash := models.HashToken(rawToken)
	var fb models.Flipbook
	if err := d.flipbooks.FindOne(ctx, bson.M{
		"access_tokens.token_hash": hash, "deleted_at": nil, "archived_at": nil,
	}).Decode(&fb); err != nil {
		return nil, err
	}
	for _, token := range fb.AccessTokens {
		if token.TokenHash == hash && token.Active(now) {
			return &fb, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (d *DB) RevokeFlipbookAccessToken(ctx context.Context, workspaceID, flipbookID, tokenID string) error {
	now := time.Now().UTC()
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{
		"_id": flipbookID, "workspace_id": workspaceID, "access_tokens.id": tokenID,
	}, bson.M{"$set": bson.M{
		"access_tokens.$.revoked_at": now, "updated_at": now,
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}
