package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var notDeleted = bson.M{"deleted_at": nil}

func (d *DB) CreateFlipbook(fb *models.Flipbook) error {
	return d.CreateFlipbookContext(context.Background(), fb)
}

func (d *DB) CreateFlipbookContext(ctx context.Context, fb *models.Flipbook) error {
	if fb.ID == "" {
		fb.ID = newID()
	}
	now := time.Now().UTC()
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = now
	}
	fb.UpdatedAt = now
	fb.ApplyDefaults()
	if fb.PublicationState == models.PublicationPublished && fb.PublishedAt == nil {
		fb.PublishedAt = &now
	}
	if fb.CurrentRevisionID == "" {
		fb.CurrentRevisionID = fb.ID + "-v1"
	}
	revision := models.Revision{
		ID: fb.CurrentRevisionID, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Number: 1, AssetVersion: fb.AssetVersion, Status: models.RevisionPending,
		SourceFilename: fb.Filename, OriginalGridFSFileID: fb.GridFSFileID,
		SourceChecksum: fb.SourceChecksum, ExternalSource: fb.ExternalSource,
		ExternalID: fb.ExternalID, CreatedBy: fb.OwnerID, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := d.revisions.InsertOne(ctx, revision); err != nil {
		return err
	}
	if _, err := d.flipbooks.InsertOne(ctx, fb); err != nil {
		_, _ = d.revisions.DeleteOne(ctx, bson.M{"_id": revision.ID})
		return err
	}
	return nil
}

func (d *DB) GetFlipbook(id string) (*models.Flipbook, error) {
	return d.GetFlipbookContext(context.Background(), id)
}

func (d *DB) GetFlipbookContext(ctx context.Context, id string) (*models.Flipbook, error) {
	var fb models.Flipbook
	if err := d.flipbooks.FindOne(ctx, bson.M{"_id": id, "deleted_at": nil}).Decode(&fb); err != nil {
		return nil, err
	}
	return &fb, nil
}

func (d *DB) GetFlipbookInWorkspace(ctx context.Context, workspaceID, id string) (*models.Flipbook, error) {
	var fb models.Flipbook
	if err := d.flipbooks.FindOne(ctx, bson.M{"_id": id, "workspace_id": workspaceID, "deleted_at": nil}).Decode(&fb); err != nil {
		return nil, err
	}
	return &fb, nil
}

func (d *DB) GetFlipbookBySlug(slug string) (*models.Flipbook, error) {
	return d.GetFlipbookBySlugInWorkspace(context.Background(), models.DefaultWorkspaceID, slug)
}

func (d *DB) GetFlipbookBySlugInWorkspace(ctx context.Context, workspaceID, slug string) (*models.Flipbook, error) {
	var fb models.Flipbook
	if err := d.flipbooks.FindOne(ctx, bson.M{"workspace_id": workspaceID, "slug": slug, "deleted_at": nil}).Decode(&fb); err != nil {
		return nil, err
	}
	return &fb, nil
}

func (d *DB) GetFlipbookByExternalID(ctx context.Context, workspaceID, source, externalID string) (*models.Flipbook, error) {
	var fb models.Flipbook
	if err := d.flipbooks.FindOne(ctx, bson.M{
		"workspace_id": workspaceID, "external_source": source, "external_id": externalID, "deleted_at": nil,
	}).Decode(&fb); err != nil {
		return nil, err
	}
	return &fb, nil
}

func (d *DB) ListFlipbooks() ([]*models.Flipbook, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	return decodeCursor[models.Flipbook](context.Background(), d.flipbooks, notDeleted, opts)
}

func (d *DB) GetFlipbooksByStatus(status string) ([]*models.Flipbook, error) {
	return decodeCursor[models.Flipbook](context.Background(), d.flipbooks, bson.M{"status": status, "deleted_at": nil})
}

type FlipbookFilter struct {
	WorkspaceID      string
	Query            string
	Status           string
	PublicationState models.PublicationState
	Visibility       models.Visibility
	Tags             []string
	CollectionID     string
	ExternalSource   string
	ExternalID       string
	Archived         *bool
	Sort             string
	Descending       bool
	Limit            int64
	Offset           int64
}

func flipbookQuery(filter FlipbookFilter) bson.M {
	query := bson.M{"deleted_at": nil}
	if filter.WorkspaceID != "" {
		query["workspace_id"] = filter.WorkspaceID
	}
	if filter.Status != "" {
		query["status"] = filter.Status
	}
	if filter.PublicationState != "" {
		query["publication_state"] = filter.PublicationState
	}
	if filter.Visibility != "" {
		query["visibility"] = filter.Visibility
	}
	if len(filter.Tags) > 0 {
		query["tags"] = bson.M{"$all": filter.Tags}
	}
	if filter.CollectionID != "" {
		query["collection_ids"] = filter.CollectionID
	}
	if filter.ExternalSource != "" {
		query["external_source"] = filter.ExternalSource
	}
	if filter.ExternalID != "" {
		query["external_id"] = filter.ExternalID
	}
	if filter.Archived != nil {
		if *filter.Archived {
			query["archived_at"] = bson.M{"$ne": nil}
		} else {
			query["archived_at"] = nil
		}
	}
	if text := strings.TrimSpace(filter.Query); text != "" {
		regex := bson.M{"$regex": regexp.QuoteMeta(text), "$options": "i"}
		query["$or"] = bson.A{
			bson.M{"title": regex}, bson.M{"description": regex}, bson.M{"slug": regex},
		}
	}
	return query
}

func (d *DB) SearchFlipbooks(ctx context.Context, filter FlipbookFilter) ([]*models.Flipbook, int64, error) {
	query := flipbookQuery(filter)
	total, err := d.flipbooks.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	sortField := "created_at"
	switch filter.Sort {
	case "", "created_at":
	case "updated_at", "title", "published_at", "page_count":
		sortField = filter.Sort
	default:
		return nil, 0, fmt.Errorf("unsupported flipbook sort %q", filter.Sort)
	}
	direction := 1
	if filter.Descending || filter.Sort == "" {
		direction = -1
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return nil, 0, errors.New("flipbook limit must be between 1 and 500")
	}
	if filter.Offset < 0 {
		return nil, 0, errors.New("flipbook offset must not be negative")
	}
	opts := options.Find().SetSort(bson.D{{Key: sortField, Value: direction}}).SetLimit(limit).SetSkip(filter.Offset)
	items, err := decodeCursor[models.Flipbook](ctx, d.flipbooks, query, opts)
	return items, total, err
}

func (d *DB) UpdateStatus(id, status, errorMsg string) error {
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{"$set": bson.M{
		"status": status, "error_message": errorMsg, "updated_at": time.Now().UTC(),
	}})
	return err
}

func (d *DB) UpdateConversionResult(id string, pageCount, width, height int) error {
	now := time.Now().UTC()
	update := bson.M{"$set": bson.M{
		"page_count": pageCount, "page_width": width, "page_height": height,
		"status": models.StatusReady, "error_message": "", "converted_at": now, "updated_at": now,
	}}
	var fb models.Flipbook
	if err := d.flipbooks.FindOneAndUpdate(context.Background(), bson.M{"_id": id}, update,
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&fb); err != nil {
		return err
	}
	if fb.CurrentRevisionID == "" {
		return nil
	}
	_, err := d.revisions.UpdateByID(context.Background(), fb.CurrentRevisionID, bson.M{"$set": bson.M{
		"status": models.RevisionActive, "page_count": pageCount, "progress": 100,
		"error_message": "", "validated_at": now, "activated_at": now, "updated_at": now,
	}})
	return err
}

func (d *DB) UpdateFlipbook(id, title, description string) error {
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{"$set": bson.M{
		"title": title, "description": description, "updated_at": time.Now().UTC(),
	}})
	return err
}

// UpdateFlipbookMetadata updates all editable settings without replacing
// conversion, ownership, or lifecycle fields controlled by their own methods.
func (d *DB) UpdateFlipbookMetadata(ctx context.Context, fb *models.Flipbook) error {
	if fb.ID == "" || fb.WorkspaceID == "" {
		return errors.New("flipbook id and workspace id are required")
	}
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": fb.ID, "workspace_id": fb.WorkspaceID, "deleted_at": nil}, bson.M{"$set": bson.M{
		"title": fb.Title, "description": fb.Description, "tags": fb.Tags,
		"collection_ids": fb.CollectionIDs, "external_source": fb.ExternalSource,
		"external_id": fb.ExternalID, "language": fb.Language, "seo": fb.SEO,
		"branding": fb.Branding, "download": fb.Download, "embed": fb.Embed,
		"share": fb.Share, "cta": fb.CTA, "lead_gate": fb.LeadGate, "sales": fb.Sales,
		"visibility": fb.Visibility, "review_state": fb.ReviewState,
		"review_note": fb.ReviewNote, "reviewed_by": fb.ReviewedBy, "reviewed_at": fb.ReviewedAt,
		"publish_at": fb.PublishAt, "expires_at": fb.ExpiresAt,
		"is_public":     fb.PublicationState == models.PublicationPublished && (fb.Visibility == models.VisibilityPublic || fb.Visibility == models.VisibilityUnlisted),
		"password_hash": fb.PasswordHash, "access_tokens": fb.AccessTokens,
		"updated_at": time.Now().UTC(),
	}})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

func (d *DB) SetPublicationState(ctx context.Context, workspaceID, id string, state models.PublicationState, visibility models.Visibility, now time.Time) error {
	switch state {
	case models.PublicationDraft, models.PublicationPublished, models.PublicationUnpublished:
	default:
		return fmt.Errorf("invalid publication state %q", state)
	}
	if visibility == "" {
		visibility = models.VisibilityPrivate
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	set := bson.M{
		"publication_state": state, "visibility": visibility,
		"is_public":  state == models.PublicationPublished && (visibility == models.VisibilityPublic || visibility == models.VisibilityUnlisted),
		"updated_at": now,
	}
	if state == models.PublicationPublished {
		set["published_at"] = now
		set["unpublished_at"] = nil
	} else {
		set["unpublished_at"] = now
	}
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": id, "workspace_id": workspaceID, "deleted_at": nil}, bson.M{"$set": set})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) ListDuePublications(ctx context.Context, now time.Time) ([]*models.Flipbook, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := bson.M{"deleted_at": nil, "archived_at": nil, "$or": bson.A{
		bson.M{"publication_state": models.PublicationDraft, "publish_at": bson.M{"$lte": now, "$ne": nil}},
		bson.M{"publication_state": models.PublicationPublished, "expires_at": bson.M{"$lte": now, "$ne": nil}},
	}}
	return decodeCursor[models.Flipbook](ctx, d.flipbooks, query)
}

func (d *DB) ArchiveFlipbook(ctx context.Context, workspaceID, id, reason string) error {
	now := time.Now().UTC()
	result, err := d.flipbooks.UpdateOne(ctx, bson.M{"_id": id, "workspace_id": workspaceID, "deleted_at": nil}, bson.M{"$set": bson.M{
		"archived_at": now, "archive_reason": reason, "is_public": false, "updated_at": now,
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) RestoreFlipbook(ctx context.Context, workspaceID, id string) error {
	fb, err := d.GetFlipbookInWorkspace(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	_, err = d.flipbooks.UpdateOne(ctx, bson.M{"_id": id, "workspace_id": workspaceID}, bson.M{
		"$unset": bson.M{"archived_at": "", "archive_reason": "", "deleted_at": "", "purge_after": ""},
		"$set": bson.M{
			"is_public":  fb.PublicationState == models.PublicationPublished && (fb.Visibility == models.VisibilityPublic || fb.Visibility == models.VisibilityUnlisted),
			"updated_at": time.Now().UTC(),
		},
	})
	return err
}

func (d *DB) DeleteFlipbook(id string) error {
	return d.SoftDeleteFlipbook(context.Background(), "", id, 30*24*time.Hour)
}

func (d *DB) SoftDeleteFlipbook(ctx context.Context, workspaceID, id string, purgeDelay time.Duration) error {
	now := time.Now().UTC()
	purgeAfter := now.Add(purgeDelay)
	filter := bson.M{"_id": id, "deleted_at": nil}
	if workspaceID != "" {
		filter["workspace_id"] = workspaceID
	}
	_, err := d.flipbooks.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
		"deleted_at": now, "purge_after": purgeAfter, "is_public": false, "updated_at": now,
	}})
	return err
}

func (d *DB) ListFlipbooksPendingPurge(ctx context.Context, now time.Time) ([]*models.Flipbook, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return decodeCursor[models.Flipbook](ctx, d.flipbooks, bson.M{"deleted_at": bson.M{"$ne": nil}, "purge_after": bson.M{"$lte": now}})
}

// PurgeFlipbook removes database records. The lifecycle service must also
// remove storage and GridFS artifacts.
func (d *DB) PurgeFlipbook(ctx context.Context, workspaceID, id string) error {
	filter := bson.M{"flipbook_id": id}
	if workspaceID != "" {
		filter["workspace_id"] = workspaceID
	}
	collections := []*mongo.Collection{
		d.revisions, d.analyticsEvents, d.linkOverlays, d.leads,
		d.purchases, d.entitlements, d.jobs, d.slugRedirects,
	}
	for _, collection := range collections {
		if _, err := collection.DeleteMany(ctx, filter); err != nil {
			return err
		}
	}
	if _, err := d.views.DeleteMany(ctx, bson.M{"flipbook_id": id}); err != nil {
		return err
	}
	collectionFilter := bson.M{}
	if workspaceID != "" {
		collectionFilter["workspace_id"] = workspaceID
	}
	if _, err := d.collections.UpdateMany(ctx, collectionFilter, bson.M{"$pull": bson.M{"flipbook_ids": id}}); err != nil {
		return err
	}
	deleteFilter := bson.M{"_id": id}
	if workspaceID != "" {
		deleteFilter["workspace_id"] = workspaceID
	}
	_, err := d.flipbooks.DeleteOne(ctx, deleteFilter)
	return err
}

func (d *DB) EnsureUniqueSlug(slug string) string {
	return d.EnsureUniqueSlugInWorkspace(context.Background(), models.DefaultWorkspaceID, slug)
}

func (d *DB) EnsureUniqueSlugInWorkspace(ctx context.Context, workspaceID, slug string) string {
	unique, err := d.GenerateUniqueSlug(ctx, workspaceID, slug)
	if err != nil {
		return slug
	}
	return unique
}

func (d *DB) GenerateUniqueSlug(ctx context.Context, workspaceID, slug string) (string, error) {
	base := slug
	if base == "" {
		base = "flipbook"
		slug = base
	}
	for counter := 1; ; counter++ {
		count, err := d.flipbooks.CountDocuments(ctx, bson.M{"workspace_id": workspaceID, "slug": slug, "deleted_at": nil})
		if err != nil {
			return "", err
		}
		redirects, err := d.slugRedirects.CountDocuments(ctx, bson.M{"workspace_id": workspaceID, "slug": slug})
		if err != nil {
			return "", err
		}
		if count == 0 && redirects == 0 {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, counter+1)
	}
}

func (d *DB) SetGridFSFileID(id, gridfsFileID string) error {
	now := time.Now().UTC()
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{"$set": bson.M{
		"gridfs_file_id": gridfsFileID, "backup_status": "complete", "backup_error": "",
		"backed_up_at": now, "updated_at": now,
	}})
	return err
}
