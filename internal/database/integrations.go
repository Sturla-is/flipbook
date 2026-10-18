package database

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (d *DB) CreateIntegrationConnection(ctx context.Context, connection *models.IntegrationConnection) error {
	if connection.WorkspaceID == "" || connection.UserID == "" || connection.Provider == "" {
		return errors.New("integration workspace, user, and provider are required")
	}
	if connection.ID == "" {
		connection.ID = newID()
	}
	if connection.Status == "" {
		connection.Status = models.IntegrationPending
	}
	now := time.Now().UTC()
	if connection.CreatedAt.IsZero() {
		connection.CreatedAt = now
	}
	connection.UpdatedAt = now
	_, err := d.integrationConnections.InsertOne(ctx, connection)
	return err
}

func (d *DB) UpdateIntegrationConnection(ctx context.Context, connection *models.IntegrationConnection) error {
	result, err := d.integrationConnections.UpdateOne(ctx, bson.M{"_id": connection.ID, "workspace_id": connection.WorkspaceID}, bson.M{"$set": bson.M{
		"external_account_id": connection.ExternalAccountID, "display_name": connection.DisplayName,
		"status": connection.Status, "scopes": connection.Scopes,
		"access_token_encrypted":  connection.AccessTokenEncrypted,
		"refresh_token_encrypted": connection.RefreshTokenEncrypted,
		"token_expires_at":        connection.TokenExpiresAt, "last_verified_at": connection.LastVerifiedAt,
		"last_error": connection.LastError, "metadata": connection.Metadata,
		"disconnected_at": connection.DisconnectedAt, "updated_at": time.Now().UTC(),
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) GetIntegrationConnection(ctx context.Context, workspaceID, connectionID string) (*models.IntegrationConnection, error) {
	var connection models.IntegrationConnection
	if err := d.integrationConnections.FindOne(ctx, bson.M{"_id": connectionID, "workspace_id": workspaceID}).Decode(&connection); err != nil {
		return nil, err
	}
	return &connection, nil
}

func (d *DB) ListIntegrationConnections(ctx context.Context, workspaceID, userID string) ([]*models.IntegrationConnection, error) {
	query := bson.M{"workspace_id": workspaceID}
	if userID != "" {
		query["user_id"] = userID
	}
	opts := options.Find().SetSort(bson.D{{Key: "provider", Value: 1}, {Key: "created_at", Value: 1}})
	return decodeCursor[models.IntegrationConnection](ctx, d.integrationConnections, query, opts)
}

func (d *DB) DisconnectIntegrationConnection(ctx context.Context, workspaceID, connectionID string) error {
	now := time.Now().UTC()
	result, err := d.integrationConnections.UpdateOne(ctx, bson.M{"_id": connectionID, "workspace_id": workspaceID}, bson.M{
		"$set":   bson.M{"status": models.IntegrationDisconnected, "disconnected_at": now, "updated_at": now},
		"$unset": bson.M{"access_token_encrypted": "", "refresh_token_encrypted": "", "token_expires_at": ""},
	})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) CreateWebhookSubscription(ctx context.Context, subscription *models.WebhookSubscription) error {
	parsed, err := url.Parse(subscription.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("webhook URL must be an absolute http or https URL")
	}
	if subscription.WorkspaceID == "" || len(subscription.Events) == 0 {
		return errors.New("webhook workspace and events are required")
	}
	if subscription.ID == "" {
		subscription.ID = newID()
	}
	now := time.Now().UTC()
	if subscription.CreatedAt.IsZero() {
		subscription.CreatedAt = now
	}
	subscription.UpdatedAt = now
	_, err = d.webhookSubscriptions.InsertOne(ctx, subscription)
	return err
}

func (d *DB) ListWebhookSubscriptions(ctx context.Context, workspaceID, eventType string, enabledOnly bool) ([]*models.WebhookSubscription, error) {
	query := bson.M{"workspace_id": workspaceID}
	if enabledOnly {
		query["enabled"] = true
	}
	if eventType != "" {
		query["$or"] = bson.A{bson.M{"events": eventType}, bson.M{"events": "*"}}
	}
	return decodeCursor[models.WebhookSubscription](ctx, d.webhookSubscriptions, query)
}

func (d *DB) SetWebhookSubscriptionEnabled(ctx context.Context, workspaceID, subscriptionID string, enabled bool) error {
	now := time.Now().UTC()
	set := bson.M{"enabled": enabled, "updated_at": now}
	if !enabled {
		set["disabled_at"] = now
	} else {
		set["disabled_at"] = nil
	}
	result, err := d.webhookSubscriptions.UpdateOne(ctx, bson.M{"_id": subscriptionID, "workspace_id": workspaceID}, bson.M{"$set": set})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}

func (d *DB) EnqueueWebhookDelivery(ctx context.Context, delivery *models.WebhookDelivery) error {
	if delivery.WorkspaceID == "" || delivery.SubscriptionID == "" || delivery.EventID == "" || delivery.EventType == "" {
		return errors.New("webhook delivery workspace, subscription, event id, and type are required")
	}
	now := time.Now().UTC()
	if delivery.ID == "" {
		delivery.ID = newID()
	}
	if delivery.Status == "" {
		delivery.Status = models.WebhookPending
	}
	if delivery.NextAttemptAt.IsZero() {
		delivery.NextAttemptAt = now
	}
	if delivery.MaxAttempts == 0 {
		delivery.MaxAttempts = 8
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	delivery.UpdatedAt = now
	filter := bson.M{"workspace_id": delivery.WorkspaceID, "subscription_id": delivery.SubscriptionID, "event_id": delivery.EventID}
	_, err := d.webhookDeliveries.UpdateOne(ctx, filter, bson.M{"$setOnInsert": delivery}, options.UpdateOne().SetUpsert(true))
	return err
}

func (d *DB) LeaseWebhookDeliveries(ctx context.Context, workerID string, limit int, leaseDuration time.Duration) ([]*models.WebhookDelivery, error) {
	if workerID == "" || leaseDuration <= 0 || limit < 1 || limit > 100 {
		return nil, errors.New("invalid webhook lease arguments")
	}
	results := make([]*models.WebhookDelivery, 0, limit)
	for len(results) < limit {
		now := time.Now().UTC()
		query := bson.M{"next_attempt_at": bson.M{"$lte": now}, "$or": bson.A{
			bson.M{"status": bson.M{"$in": bson.A{models.WebhookPending, models.WebhookRetry}}},
			bson.M{"status": models.WebhookDelivering, "lease_expires_at": bson.M{"$lte": now}},
		}}
		update := bson.M{
			"$set": bson.M{"status": models.WebhookDelivering, "lease_owner": workerID, "lease_expires_at": now.Add(leaseDuration), "last_attempt_at": now, "updated_at": now},
			"$inc": bson.M{"attempts": 1},
		}
		var delivery models.WebhookDelivery
		err := d.webhookDeliveries.FindOneAndUpdate(ctx, query, update,
			options.FindOneAndUpdate().SetSort(bson.D{{Key: "next_attempt_at", Value: 1}}).SetReturnDocument(options.After)).Decode(&delivery)
		if err == mongo.ErrNoDocuments {
			break
		}
		if err != nil {
			return results, err
		}
		if delivery.Attempts > delivery.MaxAttempts {
			_, _ = d.webhookDeliveries.UpdateByID(ctx, delivery.ID, bson.M{
				"$set":   bson.M{"status": models.WebhookFailed, "last_error": "maximum attempts exceeded", "updated_at": time.Now().UTC()},
				"$unset": bson.M{"lease_owner": "", "lease_expires_at": ""},
			})
			continue
		}
		results = append(results, &delivery)
	}
	return results, nil
}

func (d *DB) CompleteWebhookDelivery(ctx context.Context, deliveryID, workerID string, responseStatus int) error {
	now := time.Now().UTC()
	result, err := d.webhookDeliveries.UpdateOne(ctx, bson.M{"_id": deliveryID, "status": models.WebhookDelivering, "lease_owner": workerID}, bson.M{
		"$set":   bson.M{"status": models.WebhookDelivered, "response_status": responseStatus, "delivered_at": now, "last_error": "", "updated_at": now},
		"$unset": bson.M{"lease_owner": "", "lease_expires_at": ""},
	})
	if err == nil && result.MatchedCount == 0 {
		return ErrConflict
	}
	return err
}

func (d *DB) FailWebhookDelivery(ctx context.Context, deliveryID, workerID, failure string, responseStatus int, baseBackoff time.Duration) error {
	delivery, err := d.GetWebhookDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}
	if delivery.Status != models.WebhookDelivering || delivery.LeaseOwner != workerID {
		return ErrConflict
	}
	now := time.Now().UTC()
	status := models.WebhookRetry
	next := now.Add(models.RetryDelay(delivery.Attempts, baseBackoff, 24*time.Hour))
	if delivery.Attempts >= delivery.MaxAttempts {
		status = models.WebhookFailed
		next = now
	}
	result, err := d.webhookDeliveries.UpdateOne(ctx, bson.M{"_id": deliveryID, "status": models.WebhookDelivering, "lease_owner": workerID}, bson.M{
		"$set":   bson.M{"status": status, "next_attempt_at": next, "response_status": responseStatus, "last_error": failure, "updated_at": now},
		"$unset": bson.M{"lease_owner": "", "lease_expires_at": ""},
	})
	if err == nil && result.MatchedCount == 0 {
		return ErrConflict
	}
	return err
}

func (d *DB) GetWebhookDelivery(ctx context.Context, deliveryID string) (*models.WebhookDelivery, error) {
	var delivery models.WebhookDelivery
	if err := d.webhookDeliveries.FindOne(ctx, bson.M{"_id": deliveryID}).Decode(&delivery); err != nil {
		return nil, err
	}
	return &delivery, nil
}

func (d *DB) ListWebhookDeliveries(ctx context.Context, workspaceID, subscriptionID string, limit int64) ([]*models.WebhookDelivery, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("webhook delivery limit must be between 1 and 1000")
	}
	query := bson.M{"workspace_id": workspaceID}
	if subscriptionID != "" {
		query["subscription_id"] = subscriptionID
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	return decodeCursor[models.WebhookDelivery](ctx, d.webhookDeliveries, query, opts)
}
