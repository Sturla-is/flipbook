package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type AnalyticsWriteOptions struct {
	Retention     time.Duration
	ConsentPolicy string
	Consented     bool
}

func (d *DB) InsertAnalyticsEvent(ctx context.Context, event *models.AnalyticsEvent, writeOptions AnalyticsWriteOptions) error {
	if event.WorkspaceID == "" || event.FlipbookID == "" || event.Type == "" {
		return errors.New("analytics workspace, flipbook, and type are required")
	}
	if writeOptions.ConsentPolicy == "" {
		writeOptions.ConsentPolicy = "required"
	}
	event.PrepareForStorage(writeOptions.Retention, writeOptions.ConsentPolicy, writeOptions.Consented)
	if event.ID == "" {
		event.ID = newID()
	}
	_, err := d.analyticsEvents.InsertOne(ctx, event)
	return err
}

func analyticsQuery(filter models.AnalyticsFilter) (bson.M, error) {
	if filter.WorkspaceID == "" {
		return nil, errors.New("analytics workspace is required")
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		return nil, errors.New("analytics from time must be before to time")
	}
	query := bson.M{"workspace_id": filter.WorkspaceID}
	if len(filter.FlipbookIDs) > 0 {
		query["flipbook_id"] = bson.M{"$in": filter.FlipbookIDs}
	}
	if len(filter.Types) > 0 {
		query["type"] = bson.M{"$in": filter.Types}
	}
	if !filter.From.IsZero() || !filter.To.IsZero() {
		rangeQuery := bson.M{}
		if !filter.From.IsZero() {
			rangeQuery["$gte"] = filter.From
		}
		if !filter.To.IsZero() {
			rangeQuery["$lt"] = filter.To
		}
		query["occurred_at"] = rangeQuery
	}
	return query, nil
}

func (d *DB) ListAnalyticsEvents(ctx context.Context, filter models.AnalyticsFilter, limit int64) ([]*models.AnalyticsEvent, error) {
	query, err := analyticsQuery(filter)
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 1000
	}
	if limit < 1 || limit > 10000 {
		return nil, errors.New("analytics limit must be between 1 and 10000")
	}
	opts := options.Find().SetSort(bson.D{{Key: "occurred_at", Value: 1}}).SetLimit(limit)
	return decodeCursor[models.AnalyticsEvent](ctx, d.analyticsEvents, query, opts)
}

func (d *DB) AggregateAnalytics(ctx context.Context, filter models.AnalyticsFilter) (models.AnalyticsSummary, error) {
	var summary models.AnalyticsSummary
	query, err := analyticsQuery(filter)
	if err != nil {
		return summary, err
	}
	countIf := func(eventType models.AnalyticsEventType) bson.M {
		return bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$type", eventType}}, 1, 0}}}
	}
	group := bson.M{
		"_id": nil, "events": bson.M{"$sum": 1},
		"qualified_reads": countIf(models.AnalyticsQualifiedRead),
		"downloads":       countIf(models.AnalyticsDownload), "leads": countIf(models.AnalyticsLead),
		"purchases": countIf(models.AnalyticsPurchase), "completions": countIf(models.AnalyticsCompletion),
		"active_seconds": bson.M{"$sum": "$active_seconds"},
		"reader_ids":     bson.M{"$addToSet": "$reader_id_hash"},
	}
	cursor, err := d.analyticsEvents.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: query}}, {{Key: "$group", Value: group}},
	})
	if err != nil {
		return summary, err
	}
	defer cursor.Close(ctx)
	if cursor.Next(ctx) {
		var row struct {
			Events         int64    `bson:"events"`
			QualifiedReads int64    `bson:"qualified_reads"`
			Downloads      int64    `bson:"downloads"`
			Leads          int64    `bson:"leads"`
			Purchases      int64    `bson:"purchases"`
			Completions    int64    `bson:"completions"`
			ActiveSeconds  int64    `bson:"active_seconds"`
			ReaderIDs      []string `bson:"reader_ids"`
		}
		if err := cursor.Decode(&row); err != nil {
			return summary, err
		}
		summary.Events = row.Events
		summary.QualifiedReads = row.QualifiedReads
		summary.Downloads = row.Downloads
		summary.Leads = row.Leads
		summary.Purchases = row.Purchases
		summary.Completions = row.Completions
		summary.ActiveSeconds = row.ActiveSeconds
		for _, readerID := range row.ReaderIDs {
			if readerID != "" {
				summary.UniqueReaders++
			}
		}
		if summary.QualifiedReads > 0 {
			summary.CompletionRate = float64(summary.Completions) / float64(summary.QualifiedReads)
		}
	}
	return summary, cursor.Err()
}

func (d *DB) AnalyticsBreakdown(ctx context.Context, filter models.AnalyticsFilter, dimension string) ([]models.AnalyticsBucket, error) {
	query, err := analyticsQuery(filter)
	if err != nil {
		return nil, err
	}
	var groupID any
	switch dimension {
	case "referrer", "device", "browser", "country", "placement":
		groupID = "$" + dimension
	case "utm_source", "utm_medium", "utm_campaign":
		groupID = "$utm." + dimension[4:]
	case "day":
		groupID = bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$occurred_at", "timezone": "UTC"}}
	default:
		return nil, fmt.Errorf("unsupported analytics dimension %q", dimension)
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: query}},
		{{Key: "$group", Value: bson.M{"_id": groupID, "count": bson.M{"$sum": 1}, "value": bson.M{"$sum": "$value"}}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}, {Key: "_id", Value: 1}}}},
		{{Key: "$limit", Value: 100}},
	}
	cursor, err := d.analyticsEvents.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	results := make([]models.AnalyticsBucket, 0)
	for cursor.Next(ctx) {
		var bucket models.AnalyticsBucket
		if err := cursor.Decode(&bucket); err != nil {
			return nil, err
		}
		if bucket.Key != "" {
			results = append(results, bucket)
		}
	}
	return results, cursor.Err()
}

func (d *DB) AnalyticsPageStats(ctx context.Context, filter models.AnalyticsFilter) ([]models.AnalyticsPageStat, error) {
	query, err := analyticsQuery(filter)
	if err != nil {
		return nil, err
	}
	query["page"] = bson.M{"$gt": 0}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: query}},
		{{Key: "$group", Value: bson.M{
			"_id":            "$page",
			"exposures":      bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$type", models.AnalyticsPageExposure}}, 1, 0}}},
			"completions":    bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$type", models.AnalyticsCompletion}}, 1, 0}}},
			"active_seconds": bson.M{"$sum": "$active_seconds"},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	}
	cursor, err := d.analyticsEvents.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	results := make([]models.AnalyticsPageStat, 0)
	for cursor.Next(ctx) {
		var stat models.AnalyticsPageStat
		if err := cursor.Decode(&stat); err != nil {
			return nil, err
		}
		results = append(results, stat)
	}
	return results, cursor.Err()
}

func (d *DB) DeleteAnalyticsForFlipbook(ctx context.Context, workspaceID, flipbookID string) error {
	_, err := d.analyticsEvents.DeleteMany(ctx, bson.M{"workspace_id": workspaceID, "flipbook_id": flipbookID})
	return err
}
