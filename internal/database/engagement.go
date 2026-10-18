package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (d *DB) CreateLinkOverlay(ctx context.Context, link *models.LinkOverlay) error {
	if err := link.Validate(); err != nil {
		return err
	}
	fb, err := d.GetFlipbookInWorkspace(ctx, link.WorkspaceID, link.FlipbookID)
	if err != nil {
		return err
	}
	if link.RevisionID == "" {
		link.RevisionID = fb.CurrentRevisionID
	}
	if link.Page > fb.PageCount && fb.PageCount > 0 {
		return errors.New("link page exceeds publication page count")
	}
	if link.Type == models.LinkInternal && link.TargetPage > fb.PageCount && fb.PageCount > 0 {
		return errors.New("link target exceeds publication page count")
	}
	if link.ID == "" {
		link.ID = newID()
	}
	now := time.Now().UTC()
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	link.UpdatedAt = now
	_, err = d.linkOverlays.InsertOne(ctx, link)
	return err
}

func (d *DB) UpdateLinkOverlay(ctx context.Context, link *models.LinkOverlay) error {
	if err := link.Validate(); err != nil {
		return err
	}
	result, err := d.linkOverlays.UpdateOne(ctx, bson.M{
		"_id": link.ID, "workspace_id": link.WorkspaceID, "flipbook_id": link.FlipbookID,
	}, bson.M{"$set": bson.M{
		"revision_id": link.RevisionID, "page": link.Page, "x": link.X, "y": link.Y,
		"width": link.Width, "height": link.Height, "type": link.Type, "url": link.URL,
		"target_page": link.TargetPage, "title": link.Title, "enabled": link.Enabled,
		"updated_at": time.Now().UTC(),
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) ListLinkOverlays(ctx context.Context, workspaceID, flipbookID, revisionID string) ([]*models.LinkOverlay, error) {
	query := bson.M{"workspace_id": workspaceID, "flipbook_id": flipbookID, "enabled": true}
	if revisionID != "" {
		query["revision_id"] = revisionID
	}
	opts := options.Find().SetSort(bson.D{{Key: "page", Value: 1}, {Key: "created_at", Value: 1}})
	return decodeCursor[models.LinkOverlay](ctx, d.linkOverlays, query, opts)
}

func (d *DB) ListLinkOverlaysForEditor(ctx context.Context, workspaceID, flipbookID, revisionID string) ([]*models.LinkOverlay, error) {
	query := bson.M{"workspace_id": workspaceID, "flipbook_id": flipbookID}
	if revisionID != "" {
		query["revision_id"] = revisionID
	}
	opts := options.Find().SetSort(bson.D{{Key: "page", Value: 1}, {Key: "created_at", Value: 1}})
	return decodeCursor[models.LinkOverlay](ctx, d.linkOverlays, query, opts)
}

func (d *DB) DeleteLinkOverlay(ctx context.Context, workspaceID, flipbookID, linkID string) error {
	_, err := d.linkOverlays.DeleteOne(ctx, bson.M{"_id": linkID, "workspace_id": workspaceID, "flipbook_id": flipbookID})
	return err
}

func (d *DB) UpsertLead(ctx context.Context, lead *models.Lead) error {
	lead.Normalize()
	if lead.WorkspaceID == "" || lead.FlipbookID == "" || lead.NormalizedEmail == "" {
		return errors.New("lead workspace, flipbook, and email are required")
	}
	now := time.Now().UTC()
	if lead.ID == "" {
		lead.ID = newID()
	}
	if lead.CreatedAt.IsZero() {
		lead.CreatedAt = now
	}
	lead.UpdatedAt = now
	if lead.Consented && lead.ConsentedAt == nil {
		lead.ConsentedAt = &now
	}
	filter := bson.M{
		"workspace_id": lead.WorkspaceID, "flipbook_id": lead.FlipbookID,
		"normalized_email": lead.NormalizedEmail, "deleted_at": nil,
	}
	set := bson.M{
		"email": lead.Email, "normalized_email": lead.NormalizedEmail, "name": lead.Name,
		"consented": lead.Consented, "consent_text": lead.ConsentText, "consented_at": lead.ConsentedAt,
		"source": lead.Source, "utm": lead.UTM, "metadata": lead.Metadata,
		"retention_expires_at": lead.RetentionExpiresAt, "updated_at": now, "deleted_at": nil,
	}
	setOnInsert := bson.M{
		"_id": lead.ID, "workspace_id": lead.WorkspaceID, "flipbook_id": lead.FlipbookID,
		"created_at": lead.CreatedAt,
	}
	_, err := d.leads.UpdateOne(ctx, filter, bson.M{"$set": set, "$setOnInsert": setOnInsert}, options.UpdateOne().SetUpsert(true))
	return err
}

type LeadFilter struct {
	WorkspaceID string
	FlipbookID  string
	From        time.Time
	To          time.Time
	Limit       int64
	Offset      int64
}

func (d *DB) ListLeads(ctx context.Context, filter LeadFilter) ([]*models.Lead, error) {
	if filter.WorkspaceID == "" {
		return nil, errors.New("lead workspace is required")
	}
	query := bson.M{"workspace_id": filter.WorkspaceID, "deleted_at": nil}
	if filter.FlipbookID != "" {
		query["flipbook_id"] = filter.FlipbookID
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
		limit = 1000
	}
	if limit < 1 || limit > 10000 || filter.Offset < 0 {
		return nil, errors.New("invalid lead pagination")
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit).SetSkip(filter.Offset)
	return decodeCursor[models.Lead](ctx, d.leads, query, opts)
}

func (d *DB) CountLeads(ctx context.Context, workspaceID, flipbookID string) (int64, error) {
	query := bson.M{"workspace_id": workspaceID, "deleted_at": nil}
	if flipbookID != "" {
		query["flipbook_id"] = flipbookID
	}
	return d.leads.CountDocuments(ctx, query)
}

func (d *DB) DeleteLead(ctx context.Context, workspaceID, leadID string) error {
	_, err := d.leads.DeleteOne(ctx, bson.M{"_id": leadID, "workspace_id": workspaceID})
	return err
}

func (d *DB) DeleteLeadsForFlipbook(ctx context.Context, workspaceID, flipbookID string) error {
	_, err := d.leads.DeleteMany(ctx, bson.M{"workspace_id": workspaceID, "flipbook_id": flipbookID})
	return err
}

func (d *DB) UpsertSubscriber(ctx context.Context, subscriber *models.Subscriber) error {
	subscriber.Normalize()
	if subscriber.WorkspaceID == "" || subscriber.NormalizedEmail == "" || subscriber.UnsubscribeTokenHash == "" {
		return errors.New("subscriber workspace, email, and unsubscribe token hash are required")
	}
	if subscriber.Status == "" {
		subscriber.Status = models.SubscriberPending
	}
	if subscriber.Status == models.SubscriberPending && (subscriber.ConfirmationTokenHash == "" || subscriber.ConfirmationExpiresAt == nil) {
		return errors.New("pending subscribers require a confirmation token hash and expiry")
	}
	now := time.Now().UTC()
	if subscriber.ID == "" {
		subscriber.ID = newID()
	}
	if subscriber.CreatedAt.IsZero() {
		subscriber.CreatedAt = now
	}
	subscriber.UpdatedAt = now
	filter := bson.M{"workspace_id": subscriber.WorkspaceID, "normalized_email": subscriber.NormalizedEmail}
	setOnInsert := bson.M{
		"_id": subscriber.ID, "created_at": subscriber.CreatedAt,
		"status": subscriber.Status, "confirmed_at": subscriber.ConfirmedAt,
		"unsubscribed_at": subscriber.UnsubscribedAt,
	}
	set := bson.M{
		"email": subscriber.Email, "source": subscriber.Source,
		"confirmation_token_hash": subscriber.ConfirmationTokenHash,
		"confirmation_expires_at": subscriber.ConfirmationExpiresAt,
		"unsubscribe_token_hash":  subscriber.UnsubscribeTokenHash, "updated_at": now,
	}
	var existing models.Subscriber
	err := d.subscribers.FindOne(ctx, filter).Decode(&existing)
	if err == nil && existing.Status == models.SubscriberUnsubscribed {
		set["status"] = models.SubscriberPending
		set["confirmed_at"] = nil
		set["unsubscribed_at"] = nil
	} else if err != nil && err != mongo.ErrNoDocuments {
		return err
	}
	_, err = d.subscribers.UpdateOne(ctx, filter, bson.M{"$setOnInsert": setOnInsert, "$set": set}, options.UpdateOne().SetUpsert(true))
	return err
}

func (d *DB) GetSubscriber(ctx context.Context, workspaceID, email string) (*models.Subscriber, error) {
	var subscriber models.Subscriber
	normalized := strings.ToLower(strings.TrimSpace(email))
	if err := d.subscribers.FindOne(ctx, bson.M{"workspace_id": workspaceID, "normalized_email": normalized}).Decode(&subscriber); err != nil {
		return nil, err
	}
	return &subscriber, nil
}

func (d *DB) ConfirmSubscriber(ctx context.Context, token string) error {
	now := time.Now().UTC()
	filter := bson.M{
		"confirmation_token_hash": models.HashToken(token), "status": models.SubscriberPending,
		"confirmation_expires_at": bson.M{"$gt": now},
	}
	result, err := d.subscribers.UpdateOne(ctx, filter, bson.M{
		"$set":   bson.M{"status": models.SubscriberSubscribed, "confirmed_at": now, "updated_at": now},
		"$unset": bson.M{"confirmation_token_hash": "", "confirmation_expires_at": ""},
	})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) UnsubscribeSubscriber(ctx context.Context, token string) error {
	now := time.Now().UTC()
	result, err := d.subscribers.UpdateOne(ctx, bson.M{"unsubscribe_token_hash": models.HashToken(token)}, bson.M{"$set": bson.M{
		"status": models.SubscriberUnsubscribed, "unsubscribed_at": now, "updated_at": now,
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) ListSubscribers(ctx context.Context, workspaceID string, status models.SubscriberStatus) ([]*models.Subscriber, error) {
	query := bson.M{"workspace_id": workspaceID}
	if status != "" {
		query["status"] = status
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	return decodeCursor[models.Subscriber](ctx, d.subscribers, query, opts)
}

func (d *DB) DeleteSubscriber(ctx context.Context, workspaceID, subscriberID string) error {
	_, err := d.subscribers.DeleteOne(ctx, bson.M{"_id": subscriberID, "workspace_id": workspaceID})
	return err
}
