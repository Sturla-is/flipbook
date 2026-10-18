package models

import (
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestFlipbookDefaultsAndBSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fb := &Flipbook{ID: "book-1", Title: "Annual report", Status: StatusReady}
	fb.ApplyDefaults()
	if fb.WorkspaceID != DefaultWorkspaceID || fb.OwnerID != LegacyAdminUserID {
		t.Fatalf("default ownership not applied: %+v", fb)
	}
	if fb.PublicationState != PublicationDraft || fb.Visibility != VisibilityPublic || fb.IsPublic {
		t.Fatalf("new publications must default to a non-public draft: %+v", fb)
	}
	if fb.AssetVersion != 1 || fb.Language != "en" || fb.Sales.Currency != "USD" {
		t.Fatalf("defaults incomplete: %+v", fb)
	}

	fb.PublicationState = PublicationPublished
	fb.Visibility = VisibilityUnlisted
	fb.IsPublic = true
	fb.Tags = []string{"finance", "annual"}
	fb.CollectionIDs = []string{"collection-1"}
	fb.SEO = SEOMetadata{Title: "SEO title", SocialDescription: "Social description", NoIndex: true}
	fb.Download = DownloadSettings{Enabled: true, AllowOriginal: true, AllowPDF: true, AllowPrint: true}
	fb.Embed = EmbedSettings{Enabled: true, AllowedDomains: []string{"example.com"}, ShowToolbar: true}
	fb.LeadGate = LeadGateSettings{Enabled: true, Mandatory: true, PreviewPages: 3, GateReading: true}
	fb.Sales = SalesSettings{Enabled: true, PriceCents: 1299, Currency: "EUR", PreviewPages: 2}
	fb.AccessTokens = []AccessToken{{ID: "token-1", TokenHash: "secret-hash", CreatedAt: now}}
	fb.CreatedAt, fb.UpdatedAt = now, now

	data, err := bson.Marshal(fb)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Flipbook
	if err := bson.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SEO.SocialDescription != "Social description" || !decoded.Download.AllowOriginal ||
		decoded.LeadGate.PreviewPages != 3 || decoded.Sales.PriceCents != 1299 ||
		len(decoded.AccessTokens) != 1 || decoded.AccessTokens[0].TokenHash != "secret-hash" {
		t.Fatalf("settings did not round-trip: %+v", decoded)
	}
	jsonData, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var jsonFields map[string]any
	if err := json.Unmarshal(jsonData, &jsonFields); err != nil {
		t.Fatal(err)
	}
	if _, exists := jsonFields["password_hash"]; exists {
		t.Fatal("password hash exposed through JSON")
	}
	tokens, ok := jsonFields["access_tokens"].([]any)
	if !ok || len(tokens) != 1 {
		t.Fatalf("access token metadata missing from JSON: %#v", jsonFields)
	}
	token := tokens[0].(map[string]any)
	if _, exists := token["token_hash"]; exists {
		t.Fatal("access token hash exposed through JSON")
	}
}

func TestFlipbookPublicationWindow(t *testing.T) {
	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	fb := Flipbook{Status: StatusReady, PublicationState: PublicationPublished, Visibility: VisibilityPublic}
	if !fb.IsPublished(now) || !fb.AllowsAnonymousAccess(now) {
		t.Fatal("ready public publication should be accessible")
	}
	fb.PublishAt = &future
	if fb.IsPublished(now) {
		t.Fatal("future publication should not be live")
	}
	fb.PublishAt = &past
	fb.ExpiresAt = &past
	if fb.IsPublished(now) {
		t.Fatal("expired publication should not be live")
	}
	fb.ExpiresAt = nil
	fb.Visibility = VisibilityPrivate
	if !fb.IsPublished(now) || fb.AllowsAnonymousAccess(now) {
		t.Fatal("private publication requires an access grant")
	}
	fb.ArchivedAt = &past
	if fb.IsPublished(now) {
		t.Fatal("archived publication should not be live")
	}
}

func TestNormalizeSlug(t *testing.T) {
	if got := NormalizeSlug("  Héllo, Annual Report / 2026  "); got != "héllo-annual-report-2026" {
		t.Fatalf("unexpected slug %q", got)
	}
	if got := NormalizeSlug("---"); got != "" {
		t.Fatalf("expected empty slug, got %q", got)
	}
}
