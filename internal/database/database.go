package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrNotFound  = mongo.ErrNoDocuments
	ErrConflict  = errors.New("resource conflict")
	ErrForbidden = errors.New("insufficient permissions")
	ErrLastOwner = errors.New("cannot remove the last workspace owner")
)

type DB struct {
	client   *mongo.Client
	database *mongo.Database
	gridfs   *mongo.GridFSBucket

	flipbooks              *mongo.Collection
	views                  *mongo.Collection
	settings               *mongo.Collection
	sessions               *mongo.Collection
	revisions              *mongo.Collection
	users                  *mongo.Collection
	workspaces             *mongo.Collection
	memberships            *mongo.Collection
	invitations            *mongo.Collection
	apiKeys                *mongo.Collection
	auditEvents            *mongo.Collection
	jobs                   *mongo.Collection
	analyticsEvents        *mongo.Collection
	linkOverlays           *mongo.Collection
	leads                  *mongo.Collection
	subscribers            *mongo.Collection
	purchases              *mongo.Collection
	entitlements           *mongo.Collection
	integrationConnections *mongo.Collection
	webhookSubscriptions   *mongo.Collection
	webhookDeliveries      *mongo.Collection
	collections            *mongo.Collection
	slugRedirects          *mongo.Collection
	schemaMigrations       *mongo.Collection
}

func Open(ctx context.Context, uri, dbName string) (*DB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	db := client.Database(dbName)
	d := &DB{client: client, database: db, gridfs: db.GridFSBucket()}
	collections := map[string]**mongo.Collection{
		"flipbooks": &d.flipbooks, "views": &d.views, "settings": &d.settings,
		"sessions": &d.sessions, "revisions": &d.revisions, "users": &d.users,
		"workspaces": &d.workspaces, "memberships": &d.memberships,
		"invitations": &d.invitations, "api_keys": &d.apiKeys,
		"audit_events": &d.auditEvents, "jobs": &d.jobs,
		"analytics_events": &d.analyticsEvents, "link_overlays": &d.linkOverlays,
		"leads": &d.leads, "subscribers": &d.subscribers, "purchases": &d.purchases,
		"entitlements": &d.entitlements, "integration_connections": &d.integrationConnections,
		"webhook_subscriptions": &d.webhookSubscriptions, "webhook_deliveries": &d.webhookDeliveries,
		"collections": &d.collections, "slug_redirects": &d.slugRedirects,
		"schema_migrations": &d.schemaMigrations,
	}
	for name, target := range collections {
		*target = db.Collection(name)
	}

	if err := d.Migrate(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("database migration: %w", err)
	}
	if err := d.EnsureIndexes(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("database indexes: %w", err)
	}
	return d, nil
}

func (d *DB) Close(ctx context.Context) error {
	return d.client.Disconnect(ctx)
}

func newID() string { return uuid.NewString() }

func decodeCursor[T any](ctx context.Context, collection *mongo.Collection, filter any, opts ...options.Lister[options.FindOptions]) ([]*T, error) {
	cursor, err := collection.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	results := make([]*T, 0)
	for cursor.Next(ctx) {
		var value T
		if err := cursor.Decode(&value); err != nil {
			return nil, err
		}
		results = append(results, &value)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
