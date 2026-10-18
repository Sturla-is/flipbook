package database

import (
	"context"
	"errors"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (d *DB) CreateRevision(ctx context.Context, revision *models.Revision) error {
	if revision.FlipbookID == "" || revision.WorkspaceID == "" {
		return errors.New("revision flipbook and workspace are required")
	}
	fb, err := d.GetFlipbookInWorkspace(ctx, revision.WorkspaceID, revision.FlipbookID)
	if err != nil {
		return err
	}
	if revision.ID == "" {
		revision.ID = newID()
	}
	if revision.Status == "" {
		revision.Status = models.RevisionPending
	}
	autoNumber := revision.Number == 0
	autoAssetVersion := revision.AssetVersion == 0
	for attempt := 0; attempt < 8; attempt++ {
		var latest models.Revision
		err := d.revisions.FindOne(ctx, bson.M{"flipbook_id": revision.FlipbookID}, options.FindOne().SetSort(bson.D{{Key: "number", Value: -1}})).Decode(&latest)
		if err != nil && err != mongo.ErrNoDocuments {
			return err
		}
		if autoNumber {
			revision.Number = latest.Number + 1
		}
		if autoAssetVersion {
			revision.AssetVersion = fb.AssetVersion + 1
			if latest.AssetVersion >= revision.AssetVersion {
				revision.AssetVersion = latest.AssetVersion + 1
			}
		}
		now := time.Now().UTC()
		if revision.CreatedAt.IsZero() {
			revision.CreatedAt = now
		}
		revision.UpdatedAt = now
		_, err = d.revisions.InsertOne(ctx, revision)
		if err == nil {
			return nil
		}
		if !mongo.IsDuplicateKeyError(err) || (!autoNumber && !autoAssetVersion) {
			return err
		}
	}
	return ErrConflict
}

func (d *DB) GetRevision(ctx context.Context, workspaceID, revisionID string) (*models.Revision, error) {
	var revision models.Revision
	if err := d.revisions.FindOne(ctx, bson.M{"_id": revisionID, "workspace_id": workspaceID, "deleted_at": nil}).Decode(&revision); err != nil {
		return nil, err
	}
	return &revision, nil
}

func (d *DB) GetCurrentRevision(ctx context.Context, workspaceID, flipbookID string) (*models.Revision, error) {
	fb, err := d.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, err
	}
	return d.GetRevision(ctx, workspaceID, fb.CurrentRevisionID)
}

func (d *DB) ListRevisions(ctx context.Context, workspaceID, flipbookID string) ([]*models.Revision, error) {
	opts := options.Find().SetSort(bson.D{{Key: "number", Value: -1}})
	return decodeCursor[models.Revision](ctx, d.revisions, bson.M{
		"workspace_id": workspaceID, "flipbook_id": flipbookID, "deleted_at": nil,
	}, opts)
}

func (d *DB) UpdateRevisionProgress(ctx context.Context, revisionID string, progress int, message string) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	_, err := d.revisions.UpdateByID(ctx, revisionID, bson.M{"$set": bson.M{
		"progress": progress, "progress_message": message, "updated_at": time.Now().UTC(),
	}})
	return err
}

func (d *DB) MarkRevisionReady(ctx context.Context, revisionID, manifestKey string, pageCount int, warnings []string) error {
	now := time.Now().UTC()
	result, err := d.revisions.UpdateOne(ctx, bson.M{"_id": revisionID, "deleted_at": nil}, bson.M{"$set": bson.M{
		"status": models.RevisionReady, "manifest_key": manifestKey, "page_count": pageCount,
		"warnings": warnings, "progress": 100, "error_message": "", "validated_at": now, "updated_at": now,
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) MarkRevisionFailed(ctx context.Context, revisionID, errorMessage string) error {
	_, err := d.revisions.UpdateByID(ctx, revisionID, bson.M{"$set": bson.M{
		"status": models.RevisionFailed, "error_message": errorMessage, "updated_at": time.Now().UTC(),
	}})
	return err
}

// ActivateRevision atomically changes the flipbook's active revision pointer.
// Revision status bookkeeping is secondary; a failed pointer update leaves the
// previously active revision live.
func (d *DB) ActivateRevision(ctx context.Context, workspaceID, flipbookID, revisionID string) error {
	var active models.Revision
	if err := d.revisions.FindOne(ctx, bson.M{
		"_id": revisionID, "workspace_id": workspaceID, "flipbook_id": flipbookID,
		"status": bson.M{"$in": bson.A{models.RevisionReady, models.RevisionActive, models.RevisionSuperseded}}, "deleted_at": nil,
	}).Decode(&active); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": flipbookID, "workspace_id": workspaceID, "deleted_at": nil}, bson.M{"$set": bson.M{
		"current_revision_id": revisionID, "asset_version": active.AssetVersion,
		"page_count": active.PageCount, "status": models.StatusReady, "error_message": "",
		"converted_at": now, "updated_at": now,
	}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	if _, err := d.revisions.UpdateMany(ctx, bson.M{
		"flipbook_id": flipbookID, "_id": bson.M{"$ne": revisionID}, "status": models.RevisionActive,
	}, bson.M{"$set": bson.M{"status": models.RevisionSuperseded, "updated_at": now}}); err != nil {
		return err
	}
	_, err = d.revisions.UpdateByID(ctx, revisionID, bson.M{"$set": bson.M{
		"status": models.RevisionActive, "activated_at": now, "updated_at": now,
	}})
	return err
}

func (d *DB) DeleteRevision(ctx context.Context, workspaceID, flipbookID, revisionID string) error {
	fb, err := d.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return err
	}
	if fb.CurrentRevisionID == revisionID {
		return ErrConflict
	}
	now := time.Now().UTC()
	_, err = d.revisions.UpdateOne(ctx, bson.M{"_id": revisionID, "workspace_id": workspaceID, "flipbook_id": flipbookID}, bson.M{"$set": bson.M{
		"deleted_at": now, "updated_at": now,
	}})
	return err
}
