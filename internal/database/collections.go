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

func (d *DB) CreateCollection(ctx context.Context, collection *models.Collection) error {
	if collection.WorkspaceID == "" || collection.Name == "" {
		return errors.New("collection workspace and name are required")
	}
	if collection.Slug == "" {
		collection.Slug = models.NormalizeSlug(collection.Name)
	}
	collection.Slug = models.NormalizeSlug(collection.Slug)
	if collection.Slug == "" {
		return errors.New("collection slug is required")
	}
	if collection.Visibility == "" {
		collection.Visibility = models.VisibilityPrivate
	}
	if collection.FlipbookIDs == nil {
		collection.FlipbookIDs = []string{}
	}
	if collection.ID == "" {
		collection.ID = newID()
	}
	now := time.Now().UTC()
	if collection.CreatedAt.IsZero() {
		collection.CreatedAt = now
	}
	collection.UpdatedAt = now
	_, err := d.collections.InsertOne(ctx, collection)
	return err
}

func (d *DB) GetCollection(ctx context.Context, workspaceID, collectionID string) (*models.Collection, error) {
	var collection models.Collection
	if err := d.collections.FindOne(ctx, bson.M{"_id": collectionID, "workspace_id": workspaceID}).Decode(&collection); err != nil {
		return nil, err
	}
	return &collection, nil
}

func (d *DB) GetCollectionBySlug(ctx context.Context, workspaceID, slug string) (*models.Collection, error) {
	var collection models.Collection
	if err := d.collections.FindOne(ctx, bson.M{"workspace_id": workspaceID, "slug": models.NormalizeSlug(slug)}).Decode(&collection); err != nil {
		return nil, err
	}
	return &collection, nil
}

func (d *DB) UpdateCollection(ctx context.Context, collection *models.Collection) error {
	collection.Slug = models.NormalizeSlug(collection.Slug)
	if collection.Slug == "" {
		return errors.New("collection slug is required")
	}
	result, err := d.collections.UpdateOne(ctx, bson.M{"_id": collection.ID, "workspace_id": collection.WorkspaceID}, bson.M{"$set": bson.M{
		"name": collection.Name, "slug": collection.Slug, "description": collection.Description,
		"visibility": collection.Visibility, "cover_flipbook_id": collection.CoverFlipbookID,
		"seo": collection.SEO, "updated_at": time.Now().UTC(),
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) ListCollections(ctx context.Context, workspaceID string, visibility models.Visibility) ([]*models.Collection, error) {
	query := bson.M{"workspace_id": workspaceID}
	if visibility != "" {
		query["visibility"] = visibility
	}
	opts := options.Find().SetSort(bson.D{{Key: "name", Value: 1}})
	return decodeCursor[models.Collection](ctx, d.collections, query, opts)
}

func (d *DB) DeleteCollection(ctx context.Context, workspaceID, collectionID string) error {
	result, err := d.collections.DeleteOne(ctx, bson.M{"_id": collectionID, "workspace_id": workspaceID})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return mongo.ErrNoDocuments
	}
	_, err = d.flipbooks.UpdateMany(ctx, bson.M{"workspace_id": workspaceID, "collection_ids": collectionID}, bson.M{"$pull": bson.M{"collection_ids": collectionID}})
	return err
}

func (d *DB) AddFlipbookToCollection(ctx context.Context, workspaceID, collectionID, flipbookID string) error {
	if _, err := d.GetCollection(ctx, workspaceID, collectionID); err != nil {
		return err
	}
	if _, err := d.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID); err != nil {
		return err
	}
	if _, err := d.collections.UpdateOne(ctx, bson.M{"_id": collectionID, "workspace_id": workspaceID}, bson.M{
		"$addToSet": bson.M{"flipbook_ids": flipbookID}, "$set": bson.M{"updated_at": time.Now().UTC()},
	}); err != nil {
		return err
	}
	if _, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": flipbookID, "workspace_id": workspaceID}, bson.M{
		"$addToSet": bson.M{"collection_ids": collectionID}, "$set": bson.M{"updated_at": time.Now().UTC()},
	}); err != nil {
		_, _ = d.collections.UpdateOne(ctx, bson.M{"_id": collectionID}, bson.M{"$pull": bson.M{"flipbook_ids": flipbookID}})
		return err
	}
	return nil
}

func (d *DB) RemoveFlipbookFromCollection(ctx context.Context, workspaceID, collectionID, flipbookID string) error {
	if _, err := d.collections.UpdateOne(ctx, bson.M{"_id": collectionID, "workspace_id": workspaceID}, bson.M{
		"$pull": bson.M{"flipbook_ids": flipbookID}, "$set": bson.M{"updated_at": time.Now().UTC()},
	}); err != nil {
		return err
	}
	_, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": flipbookID, "workspace_id": workspaceID}, bson.M{
		"$pull": bson.M{"collection_ids": collectionID}, "$set": bson.M{"updated_at": time.Now().UTC()},
	})
	return err
}

func (d *DB) ChangeFlipbookSlug(ctx context.Context, workspaceID, flipbookID, newSlug string) error {
	newSlug = models.NormalizeSlug(newSlug)
	if newSlug == "" {
		return errors.New("new slug is required")
	}
	fb, err := d.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return err
	}
	if fb.Slug == newSlug {
		return nil
	}
	count, err := d.flipbooks.CountDocuments(ctx, bson.M{"workspace_id": workspaceID, "slug": newSlug, "deleted_at": nil})
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrConflict
	}
	redirectCount, err := d.slugRedirects.CountDocuments(ctx, bson.M{"workspace_id": workspaceID, "slug": newSlug})
	if err != nil {
		return err
	}
	if redirectCount > 0 {
		return ErrConflict
	}
	now := time.Now().UTC()
	redirect := models.SlugRedirect{ID: newID(), WorkspaceID: workspaceID, Slug: fb.Slug, FlipbookID: flipbookID, CreatedAt: now}
	if _, err := d.slugRedirects.InsertOne(ctx, redirect); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrConflict
		}
		return err
	}
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": flipbookID, "workspace_id": workspaceID, "slug": fb.Slug}, bson.M{"$set": bson.M{
		"slug": newSlug, "updated_at": now,
	}})
	if err != nil {
		_, _ = d.slugRedirects.DeleteOne(ctx, bson.M{"_id": redirect.ID})
		return err
	}
	if result.MatchedCount == 0 {
		_, _ = d.slugRedirects.DeleteOne(ctx, bson.M{"_id": redirect.ID})
		return ErrConflict
	}
	return nil
}

func (d *DB) GetFlipbookBySlugOrRedirect(ctx context.Context, workspaceID, slug string) (*models.Flipbook, bool, error) {
	fb, err := d.GetFlipbookBySlugInWorkspace(ctx, workspaceID, slug)
	if err == nil {
		return fb, false, nil
	}
	if err != mongo.ErrNoDocuments {
		return nil, false, err
	}
	var redirect models.SlugRedirect
	if err := d.slugRedirects.FindOne(ctx, bson.M{"workspace_id": workspaceID, "slug": slug}).Decode(&redirect); err != nil {
		return nil, false, err
	}
	fb, err = d.GetFlipbookInWorkspace(ctx, workspaceID, redirect.FlipbookID)
	if err != nil {
		return nil, false, err
	}
	return fb, true, nil
}
