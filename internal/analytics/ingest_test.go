package analytics

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

func TestBuildEventEnrichmentAndPrivacy(t *testing.T) {
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	request := RequestContext{
		Referrer:  "https://user:secret@Example.COM/read?utm_source=referrer-source&token=private#fragment",
		UserAgent: "Mozilla/5.0 (iPhone) AppleWebKit Chrome/120 Mobile Safari/601",
		Country:   "us", Consented: true, SessionID: "cookie-session-12345", ReaderID: "reader-cookie-12345",
	}
	input := IncomingEvent{
		Type: "page_view", Page: 2, DurationMS: 1500,
		Metadata: map[string]string{
			"event_id": "event-123456", "placement": "embed", "embed_host": "WWW.Example.com:443",
			"utm_source": "Newsletter", "utm_campaign": "Launch", "unknown_secret": "do-not-store",
		},
	}
	event, err := BuildEvent(input, request, EnrichOptions{
		Options:     Options{Enabled: true, Retention: time.Hour, ConsentPolicy: ConsentRequired, AllowCountry: true, ReaderIDPepper: "pepper"},
		WorkspaceID: "workspace", FlipbookID: "book", RevisionID: "revision", PageCount: 10, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != models.AnalyticsPageExposure || event.ActiveSeconds != 1 || event.Page != 2 {
		t.Fatalf("unexpected normalized event: %+v", event)
	}
	if event.Referrer != "https://example.com/read" || event.UTM.Source != "newsletter" || event.UTM.Campaign != "launch" {
		t.Fatalf("referrer/campaign enrichment failed: %+v", event)
	}
	if event.SessionIDHash == "" || event.ReaderIDHash == "" || strings.Contains(event.SessionIDHash, "cookie-session") {
		t.Fatal("identifiers were not pseudonymized")
	}
	if event.Country != "US" || event.Device != "mobile" || event.Browser != "chrome" || event.Placement != PlacementEmbed {
		t.Fatalf("context enrichment failed: %+v", event)
	}
	if event.Metadata["embed_host"] != "www.example.com" || event.Metadata["unknown_secret"] != "" {
		t.Fatalf("metadata was not constrained: %+v", event.Metadata)
	}
	if event.ID != "book:event-123456" || !event.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("retry identity or retention incorrect: %+v", event)
	}
}

func TestBuildEventConsentAndFieldConsumption(t *testing.T) {
	base := EnrichOptions{
		Options:     Options{Enabled: true, ConsentPolicy: ConsentRequired, AllowCountry: true},
		WorkspaceID: "workspace", FlipbookID: "book", PageCount: 5,
		Query: url.Values{"utm_medium": {"email"}},
	}
	request := RequestContext{UserAgent: "Mozilla/5.0 Chrome/120", SessionID: "session-12345", ReaderID: "reader-12345", Country: "GB"}
	event, err := BuildEvent(IncomingEvent{Type: "outbound_click", SessionID: "client-session-12345", Metadata: map[string]string{
		"target_url": "https://user:pass@Example.COM/private?token=secret", "link_id": "hero-link",
	}}, request, base)
	if err != nil {
		t.Fatal(err)
	}
	if event.SessionIDHash != "" || event.ReaderIDHash != "" || event.Country != "" {
		// Country is context, not an identifier, and remains available without consent.
		if event.Country != "GB" {
			t.Fatalf("unexpected consent output: %+v", event)
		}
	}
	if event.SessionIDHash != "" || event.ReaderIDHash != "" {
		t.Fatal("identifiers retained without required consent")
	}
	if event.TargetHost != "example.com" || event.UTM.Medium != "email" || event.Metadata["link_id"] != "hero-link" {
		t.Fatalf("link/UTM consumption failed: %+v", event)
	}

	base.ConsentPolicy = ConsentAnonymous
	event, err = BuildEvent(IncomingEvent{Type: "view", SessionID: "client-session-12345"}, request, base)
	if err != nil {
		t.Fatal(err)
	}
	if event.SessionIDHash == "" || event.ReaderIDHash == "" {
		t.Fatal("anonymous policy should permit pseudonymous identifiers")
	}
}

func TestCollectionFilters(t *testing.T) {
	options := Options{Enabled: true, ConsentPolicy: ConsentRequired, IgnoreAdminPreviews: true}
	valid := RequestContext{UserAgent: "Mozilla/5.0 Chrome/120"}
	if decision := DecideCollection(options, valid); !decision.Collect {
		t.Fatalf("valid reader was filtered: %+v", decision)
	}
	for name, context := range map[string]RequestContext{
		"bot": {UserAgent: "Googlebot/2.1"}, "opted_out": {UserAgent: valid.UserAgent, OptedOut: true},
		"dnt": {UserAgent: valid.UserAgent, DoNotTrack: true}, "admin": {UserAgent: valid.UserAgent, AdminPreview: true},
	} {
		t.Run(name, func(t *testing.T) {
			if decision := DecideCollection(options, context); decision.Collect {
				t.Fatal("filtered batch was accepted")
			}
		})
	}
}

func TestInvalidPublicEvents(t *testing.T) {
	options := EnrichOptions{Options: Options{Enabled: true}, WorkspaceID: "workspace", FlipbookID: "book", PageCount: 2}
	request := RequestContext{UserAgent: "Mozilla/5.0 Chrome/120"}
	for name, input := range map[string]IncomingEvent{
		"lead": {Type: "lead"}, "purchase": {Type: "purchase"}, "page": {Type: "page_view"},
		"page_bounds": {Type: "page_view", Page: 3}, "duration": {Type: "active_time", DurationMS: MaxDurationMS + 1},
		"session":  {Type: "view", SessionID: "short"},
		"event_id": {Type: "view", Metadata: map[string]string{"event_id": "bad id"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildEvent(input, request, options); err == nil {
				t.Fatal("invalid event was accepted")
			}
		})
	}
}

func TestRequestCountryAndConsentState(t *testing.T) {
	r := httptest.NewRequest("POST", "/events", nil)
	r.Header.Set("CF-IPCountry", "se")
	if got := RequestCountry(r, true); got != "SE" {
		t.Fatalf("country = %q", got)
	}
	if got := RequestCountry(r, false); got != "" {
		t.Fatal("country captured while disabled")
	}
	r.Header.Set("CF-IPCountry", "not-a-country")
	if got := RequestCountry(r, true); got != "" {
		t.Fatal("invalid country retained")
	}
}
