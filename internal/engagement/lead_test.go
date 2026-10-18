package engagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
)

func engagementFixture(t *testing.T) (*Service, *fakeStore, *fakePublisher, *fakeMailer, *models.Flipbook) {
	t.Helper()
	store := newFakeStore()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	fb := &models.Flipbook{
		ID: "book-1", WorkspaceID: "workspace-1", OwnerID: "owner-1", Title: "A guide", Slug: "a-guide",
		Status: models.StatusReady, PublicationState: models.PublicationPublished, Visibility: models.VisibilityPublic,
		PageCount: 5, LeadGate: models.LeadGateSettings{
			Enabled: true, Mandatory: true, PreviewPages: 1, GateReading: true, GateDownloads: true,
			ConsentText: "I consent to the publisher storing my email.", SuccessMessage: "Welcome back.",
		},
	}
	store.flipbooks[fb.ID] = fb
	store.workspaces[fb.WorkspaceID] = &models.Workspace{ID: fb.WorkspaceID, Name: "Example catalog", Slug: "example"}
	publisher := &fakePublisher{flipbook: fb}
	mailer := &fakeMailer{enabled: true}
	service, err := New(store, Options{
		SigningSecret: strings.Repeat("s", 32), EncryptionKey: strings.Repeat("e", 32),
		BaseURL: "https://flipbook.example", LeadRetentionDays: 30, Publisher: publisher, Mailer: mailer,
		Webhooks: config.WebhookConfig{Enabled: true, SigningSecret: strings.Repeat("w", 32), MaxAttempts: 4, TimeoutSeconds: 5, RetryBackoffSeconds: 10},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, publisher, mailer, fb
}

func TestCaptureLeadIssuesGrantAndQueuesWebhook(t *testing.T) {
	service, store, publisher, _, fb := engagementFixture(t)
	store.webhooks["hook-1"] = &models.WebhookSubscription{
		ID: "hook-1", WorkspaceID: fb.WorkspaceID, URL: "https://hooks.example/lead", Events: []string{EventLeadCaptured}, Enabled: true,
	}
	result, err := service.CaptureLead(context.Background(), LeadInput{
		FlipbookID: fb.ID, Email: " Reader@Example.com ", Name: "Example Reader", Consent: true,
		Access: publishing.AccessRequest{Grant: "existing"}, UTM: models.UTMCampaign{Source: "newsletter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !publisher.captured || publisher.dismissed || publisher.calls != 1 || publisher.access.Grant != "existing" {
		t.Fatalf("unexpected lead grant call: %+v", publisher)
	}
	if result.Grant == nil || result.Grant.Cookie == nil || result.Message != "Welcome back." {
		t.Fatalf("missing grant or configured success message: %+v", result)
	}
	if result.Lead.NormalizedEmail != "reader@example.com" || result.Lead.RetentionExpiresAt == nil {
		t.Fatalf("lead was not normalized or retained correctly: %+v", result.Lead)
	}
	if got := result.Lead.RetentionExpiresAt.Sub(service.timeNow()); got != 30*24*time.Hour {
		t.Fatalf("retention = %v, want 30 days", got)
	}
	if len(store.deliveries) != 1 {
		t.Fatalf("webhook deliveries = %d, want 1", len(store.deliveries))
	}
	for _, delivery := range store.deliveries {
		if delivery.EventType != EventLeadCaptured || delivery.MaxAttempts != 4 {
			t.Fatalf("unexpected delivery: %+v", delivery)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(delivery.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["type"] != EventLeadCaptured || !strings.Contains(delivery.PayloadJSON, "reader@example.com") {
			t.Fatalf("unexpected webhook payload: %s", delivery.PayloadJSON)
		}
		if strings.Contains(delivery.PayloadJSON, "wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww") {
			t.Fatal("webhook signing secret appeared in payload")
		}
	}
}

func TestCaptureLeadRequiresConsent(t *testing.T) {
	service, store, publisher, _, fb := engagementFixture(t)
	_, err := service.CaptureLead(context.Background(), LeadInput{FlipbookID: fb.ID, Email: "reader@example.com"})
	if !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("error = %v, want consent required", err)
	}
	if publisher.calls != 0 || len(store.leads) != 0 {
		t.Fatal("consent-free lead reached grant or storage")
	}
}

func TestValidateLeadGate(t *testing.T) {
	valid := models.LeadGateSettings{Enabled: true, Mandatory: true, GateReading: true, GateDownloads: true, PreviewPages: 2}
	if err := ValidateLeadGate(valid, 3); err != nil {
		t.Fatal(err)
	}
	for name, settings := range map[string]models.LeadGateSettings{
		"no resource":            {Enabled: true, Mandatory: true},
		"no mode":                {Enabled: true, GateReading: true},
		"conflicting mode":       {Enabled: true, Mandatory: true, Dismissible: true, GateReading: true},
		"too many preview pages": {Enabled: true, Mandatory: true, GateReading: true, PreviewPages: 4},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateLeadGate(settings, 3); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}
		})
	}
}

func TestLeadRetentionAndCSVSafety(t *testing.T) {
	service, store, _, _, fb := engagementFixture(t)
	lead := &models.Lead{ID: "lead-1", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: "=1+1", Consented: true, CreatedAt: service.timeNow()}
	store.leads[lead.ID] = lead
	if err := service.SetLeadRetentionDays(context.Background(), fb.WorkspaceID, 7, true); err != nil {
		t.Fatal(err)
	}
	if lead.RetentionExpiresAt == nil || lead.RetentionExpiresAt.Sub(service.timeNow()) != 7*24*time.Hour {
		t.Fatalf("retention was not applied to existing lead: %+v", lead.RetentionExpiresAt)
	}
	var output bytes.Buffer
	if err := service.ExportLeadsCSV(context.Background(), &output, database.LeadFilter{WorkspaceID: fb.WorkspaceID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "'=1+1") {
		t.Fatalf("CSV formula was not neutralized: %s", output.String())
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	if allowed, _ := limiter.Allow("scope", "client"); !allowed {
		t.Fatal("first request was limited")
	}
	if allowed, _ := limiter.Allow("scope", "client"); !allowed {
		t.Fatal("second request was limited")
	}
	if allowed, retry := limiter.Allow("scope", "client"); allowed || retry <= 0 {
		t.Fatalf("third request = %v, %v", allowed, retry)
	}
	if allowed, _ := limiter.Allow("scope", "other-client"); !allowed {
		t.Fatal("independent client was limited")
	}
}
