package database

import (
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestFlipbookQueryScopesAndEscapesSearch(t *testing.T) {
	archived := false
	filter := FlipbookFilter{
		WorkspaceID: "workspace", Query: "annual.$*", Status: models.StatusReady,
		PublicationState: models.PublicationPublished, Tags: []string{"finance", "2026"},
		CollectionID: "collection", Archived: &archived,
	}
	query := flipbookQuery(filter)
	if query["workspace_id"] != "workspace" || query["status"] != models.StatusReady || query["deleted_at"] != nil {
		t.Fatalf("scope filters missing: %#v", query)
	}
	or, ok := query["$or"].(bson.A)
	if !ok || len(or) != 3 {
		t.Fatalf("search fields missing: %#v", query)
	}
	first := or[0].(bson.M)["title"].(bson.M)
	if first["$regex"] != "annual\\.\\$\\*" {
		t.Fatalf("search regex was not escaped: %#v", first)
	}
}

func TestAnalyticsAndPurchaseQueryValidation(t *testing.T) {
	if _, err := analyticsQuery(models.AnalyticsFilter{}); err == nil {
		t.Fatal("analytics query allowed an unscoped workspace")
	}
	now := time.Now()
	if _, err := analyticsQuery(models.AnalyticsFilter{WorkspaceID: "workspace", From: now, To: now}); err == nil {
		t.Fatal("analytics query allowed an empty date range")
	}
	query, err := analyticsQuery(models.AnalyticsFilter{
		WorkspaceID: "workspace", FlipbookIDs: []string{"a", "b"},
		Types: []models.AnalyticsEventType{models.AnalyticsQualifiedRead}, From: now.Add(-time.Hour), To: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if query["workspace_id"] != "workspace" || query["flipbook_id"] == nil || query["type"] == nil || query["occurred_at"] == nil {
		t.Fatalf("analytics filters missing: %#v", query)
	}
	purchases := purchaseQuery(PurchaseFilter{WorkspaceID: "workspace", Status: models.PurchasePaid, From: now.Add(-time.Hour), To: now})
	if purchases["workspace_id"] != "workspace" || purchases["status"] != models.PurchasePaid || purchases["created_at"] == nil {
		t.Fatalf("purchase filters missing: %#v", purchases)
	}
}
