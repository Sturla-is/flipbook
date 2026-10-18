package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func index(keys bson.D, configure ...func(*options.IndexOptionsBuilder)) mongo.IndexModel {
	opts := options.Index()
	for _, apply := range configure {
		apply(opts)
	}
	return mongo.IndexModel{Keys: keys, Options: opts}
}

func unique(opts *options.IndexOptionsBuilder)  { opts.SetUnique(true) }
func sparse(opts *options.IndexOptionsBuilder)  { opts.SetSparse(true) }
func ttlZero(opts *options.IndexOptionsBuilder) { opts.SetExpireAfterSeconds(0) }
func notDeletedPartial(opts *options.IndexOptionsBuilder) {
	opts.SetPartialFilterExpression(bson.M{"deleted_at": nil})
}
func fieldExistsPartial(field string) func(*options.IndexOptionsBuilder) {
	return func(opts *options.IndexOptionsBuilder) {
		opts.SetPartialFilterExpression(bson.M{field: bson.M{"$exists": true}})
	}
}

func (d *DB) EnsureIndexes(ctx context.Context) error {
	checks := []struct {
		name   string
		create func() error
	}{
		{"flipbooks", func() error {
			return createIndexes(ctx, d.flipbooks, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "slug", Value: 1}}, unique, notDeletedPartial),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "status", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "publication_state", Value: 1}, {Key: "publish_at", Value: 1}}),
				index(bson.D{{Key: "publication_state", Value: 1}, {Key: "expires_at", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "tags", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "collection_ids", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "external_source", Value: 1}, {Key: "external_id", Value: 1}}, unique, fieldExistsPartial("external_id")),
				index(bson.D{{Key: "purge_after", Value: 1}}, sparse),
				index(bson.D{{Key: "access_tokens.token_hash", Value: 1}}, sparse),
			})
		}},
		{"views", func() error {
			return createIndexes(ctx, d.views, []mongo.IndexModel{
				index(bson.D{{Key: "flipbook_id", Value: 1}, {Key: "viewed_at", Value: -1}}),
			})
		}},
		{"sessions", func() error {
			return createIndexes(ctx, d.sessions, []mongo.IndexModel{
				index(bson.D{{Key: "expires_at", Value: 1}}, ttlZero),
				index(bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}}),
			})
		}},
		{"revisions", func() error {
			return createIndexes(ctx, d.revisions, []mongo.IndexModel{
				index(bson.D{{Key: "flipbook_id", Value: 1}, {Key: "number", Value: 1}}, unique),
				index(bson.D{{Key: "flipbook_id", Value: 1}, {Key: "asset_version", Value: 1}}, unique),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "status", Value: 1}}),
			})
		}},
		{"users", func() error {
			return createIndexes(ctx, d.users, []mongo.IndexModel{
				index(bson.D{{Key: "normalized_email", Value: 1}}, unique, sparse),
				index(bson.D{{Key: "oidc_issuer", Value: 1}, {Key: "oidc_subject", Value: 1}}, unique, sparse),
			})
		}},
		{"workspaces", func() error {
			return createIndexes(ctx, d.workspaces, []mongo.IndexModel{
				index(bson.D{{Key: "slug", Value: 1}}, unique),
				index(bson.D{{Key: "settings.custom_domains.host", Value: 1}}, sparse),
			})
		}},
		{"memberships", func() error {
			return createIndexes(ctx, d.memberships, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "user_id", Value: 1}}, unique),
				index(bson.D{{Key: "user_id", Value: 1}, {Key: "workspace_id", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "role", Value: 1}}),
			})
		}},
		{"invitations", func() error {
			return createIndexes(ctx, d.invitations, []mongo.IndexModel{
				index(bson.D{{Key: "token_hash", Value: 1}}, unique),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "normalized_email", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "expires_at", Value: 1}}),
			})
		}},
		{"api keys", func() error {
			return createIndexes(ctx, d.apiKeys, []mongo.IndexModel{
				index(bson.D{{Key: "key_hash", Value: 1}}, unique),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "user_id", Value: 1}}),
			})
		}},
		{"audit events", func() error {
			return createIndexes(ctx, d.auditEvents, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "resource_type", Value: 1}, {Key: "resource_id", Value: 1}, {Key: "created_at", Value: -1}}),
			})
		}},
		{"jobs", func() error {
			return createIndexes(ctx, d.jobs, []mongo.IndexModel{
				index(bson.D{{Key: "status", Value: 1}, {Key: "run_at", Value: 1}, {Key: "priority", Value: -1}}),
				index(bson.D{{Key: "status", Value: 1}, {Key: "lease_expires_at", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "flipbook_id", Value: 1}, {Key: "status", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "idempotency_key", Value: 1}}, unique, fieldExistsPartial("idempotency_key")),
			})
		}},
		{"analytics events", func() error {
			return createIndexes(ctx, d.analyticsEvents, []mongo.IndexModel{
				index(bson.D{{Key: "expires_at", Value: 1}}, ttlZero),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "occurred_at", Value: -1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "flipbook_id", Value: 1}, {Key: "occurred_at", Value: -1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "type", Value: 1}, {Key: "occurred_at", Value: -1}}),
				index(bson.D{{Key: "flipbook_id", Value: 1}, {Key: "type", Value: 1}, {Key: "page", Value: 1}}),
				index(bson.D{{Key: "reader_id_hash", Value: 1}}, sparse),
			})
		}},
		{"link overlays", func() error {
			return createIndexes(ctx, d.linkOverlays, []mongo.IndexModel{
				index(bson.D{{Key: "flipbook_id", Value: 1}, {Key: "revision_id", Value: 1}, {Key: "page", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "flipbook_id", Value: 1}}),
			})
		}},
		{"leads", func() error {
			return createIndexes(ctx, d.leads, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "flipbook_id", Value: 1}, {Key: "normalized_email", Value: 1}}, unique, notDeletedPartial),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "retention_expires_at", Value: 1}}, ttlZero, sparse),
			})
		}},
		{"subscribers", func() error {
			return createIndexes(ctx, d.subscribers, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "normalized_email", Value: 1}}, unique),
				index(bson.D{{Key: "confirmation_token_hash", Value: 1}}, unique, sparse),
				index(bson.D{{Key: "unsubscribe_token_hash", Value: 1}}, unique, sparse),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "status", Value: 1}, {Key: "created_at", Value: -1}}),
			})
		}},
		{"purchases", func() error {
			return createIndexes(ctx, d.purchases, []mongo.IndexModel{
				index(bson.D{{Key: "stripe_checkout_session_id", Value: 1}}, unique, sparse),
				index(bson.D{{Key: "stripe_event_id", Value: 1}}, unique, sparse),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "flipbook_id", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "status", Value: 1}, {Key: "created_at", Value: -1}}),
				index(bson.D{{Key: "email_hash", Value: 1}}, sparse),
			})
		}},
		{"entitlements", func() error {
			return createIndexes(ctx, d.entitlements, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "flipbook_id", Value: 1}, {Key: "user_id", Value: 1}}, unique, fieldExistsPartial("user_id")),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "flipbook_id", Value: 1}, {Key: "email_hash", Value: 1}}, unique, fieldExistsPartial("email_hash")),
				index(bson.D{{Key: "purchase_id", Value: 1}}, unique),
				index(bson.D{{Key: "status", Value: 1}, {Key: "expires_at", Value: 1}}),
			})
		}},
		{"integration connections", func() error {
			return createIndexes(ctx, d.integrationConnections, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "provider", Value: 1}, {Key: "external_account_id", Value: 1}}, unique, fieldExistsPartial("external_account_id")),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "user_id", Value: 1}, {Key: "provider", Value: 1}}),
			})
		}},
		{"webhook subscriptions", func() error {
			return createIndexes(ctx, d.webhookSubscriptions, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "enabled", Value: 1}}),
			})
		}},
		{"webhook deliveries", func() error {
			return createIndexes(ctx, d.webhookDeliveries, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "subscription_id", Value: 1}, {Key: "event_id", Value: 1}}, unique),
				index(bson.D{{Key: "status", Value: 1}, {Key: "next_attempt_at", Value: 1}}),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "created_at", Value: -1}}),
			})
		}},
		{"collections", func() error {
			return createIndexes(ctx, d.collections, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "slug", Value: 1}}, unique),
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "visibility", Value: 1}}),
				index(bson.D{{Key: "flipbook_ids", Value: 1}}),
			})
		}},
		{"slug redirects", func() error {
			return createIndexes(ctx, d.slugRedirects, []mongo.IndexModel{
				index(bson.D{{Key: "workspace_id", Value: 1}, {Key: "slug", Value: 1}}, unique),
				index(bson.D{{Key: "flipbook_id", Value: 1}}),
			})
		}},
	}
	for _, check := range checks {
		if err := check.create(); err != nil {
			return fmt.Errorf("%s: %w", check.name, err)
		}
	}
	return nil
}

func createIndexes(ctx context.Context, collection *mongo.Collection, indexes []mongo.IndexModel) error {
	if len(indexes) == 0 {
		return nil
	}
	_, err := collection.Indexes().CreateMany(ctx, indexes)
	return err
}
