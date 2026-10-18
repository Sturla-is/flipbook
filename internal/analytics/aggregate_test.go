package analytics

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

func TestBuildReportAggregation(t *testing.T) {
	day := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	event := func(id, book, session, reader string, eventType models.AnalyticsEventType) *models.AnalyticsEvent {
		return &models.AnalyticsEvent{
			ID: id, WorkspaceID: "workspace", FlipbookID: book, Type: eventType, OccurredAt: day,
			SessionIDHash: session, ReaderIDHash: reader, Device: "desktop", Browser: "chrome",
			Placement: PlacementStandalone, UTM: models.UTMCampaign{Source: "newsletter", Campaign: "launch"},
		}
	}
	events := []*models.AnalyticsEvent{}
	add := func(event *models.AnalyticsEvent) { events = append(events, event) }
	add(event("v1", "fb1", "s1", "r1", models.AnalyticsView))
	add(event("v1-duplicate", "fb1", "s1", "r1", models.AnalyticsView))
	add(event("q1", "fb1", "s1", "r1", models.AnalyticsQualifiedRead))
	add(event("q1-duplicate", "fb1", "s1", "r1", models.AnalyticsQualifiedRead))
	p1 := event("p1", "fb1", "s1", "r1", models.AnalyticsPageExposure)
	p1.Page, p1.ActiveSeconds = 1, 10
	add(p1)
	p2 := event("p2", "fb1", "s1", "r1", models.AnalyticsPageExposure)
	p2.Page, p2.ActiveSeconds = 2, 20
	add(p2)
	active := event("a1", "fb1", "s1", "r1", models.AnalyticsActiveTime)
	active.Page, active.ActiveSeconds = 2, 5
	add(active)
	completion := event("c1", "fb1", "s1", "r1", models.AnalyticsCompletion)
	completion.Page = 2
	add(completion)
	cta := event("cta1", "fb1", "s1", "r1", models.AnalyticsCTAClick)
	cta.Page, cta.TargetHost, cta.Metadata = 2, "example.com", map[string]string{"cta_id": "hero"}
	add(cta)
	add(event("out1", "fb1", "s1", "r1", models.AnalyticsOutboundClick))
	download := event("d1", "fb1", "s1", "r1", models.AnalyticsDownload)
	download.Metadata = map[string]string{"format": "pdf"}
	add(download)
	search := event("search1", "fb1", "s1", "r1", models.AnalyticsSearch)
	search.SearchTerms = "quarterly report"
	add(search)
	share := event("share1", "fb1", "s1", "r1", models.AnalyticsShare)
	share.Metadata = map[string]string{"channel": "email"}
	add(share)
	add(event("lead1", "fb1", "s1", "r1", models.AnalyticsLead))
	add(event("purchase1", "fb1", "s1", "r1", models.AnalyticsPurchase))

	add(event("v2", "fb1", "s2", "r2", models.AnalyticsView))
	add(event("q2", "fb1", "s2", "r2", models.AnalyticsQualifiedRead))
	secondPage := event("p3", "fb1", "s2", "r2", models.AnalyticsPageExposure)
	secondPage.Page, secondPage.ActiveSeconds = 1, 5
	add(secondPage)
	add(event("v3", "fb2", "s3", "r3", models.AnalyticsView))
	thirdPage := event("p4", "fb2", "s3", "r3", models.AnalyticsPageExposure)
	thirdPage.Page, thirdPage.ActiveSeconds = 1, 5
	add(thirdPage)

	report := BuildReport(ReportInput{
		WorkspaceID: "workspace", Range: Range{From: day.Add(-24 * time.Hour), To: day.Add(24 * time.Hour)},
		Publications: []Publication{{ID: "fb1", Title: "First", PageCount: 2}, {ID: "fb2", Title: "Second", PageCount: 1}},
		Events:       events, LegacyViews: map[string]int64{"fb1": 7, "fb2": 3}, Now: day,
	})
	totals := report.Totals
	if totals.Views != 3 || totals.QualifiedReads != 2 || totals.Completions != 1 || totals.CompletionRate != 0.5 {
		t.Fatalf("deduplicated funnel totals are wrong: %+v", totals)
	}
	if totals.PageViews != 4 || totals.Sessions != 3 || totals.UniqueReaders != 3 || totals.ActiveSeconds != 45 {
		t.Fatalf("reading totals are wrong: %+v", totals)
	}
	if totals.CTAClicks != 1 || totals.OutboundClicks != 1 || totals.Downloads != 1 || totals.Searches != 1 || totals.Shares != 1 || totals.Leads != 1 || totals.Purchases != 1 {
		t.Fatalf("engagement totals are wrong: %+v", totals)
	}
	if totals.LegacyViews != 10 {
		t.Fatalf("legacy views should remain a separate all-time metric: %+v", totals)
	}
	if len(report.Trends) != 2 || report.Trends[1].Views != 3 || report.Trends[1].Sessions != 3 || report.Trends[1].ActiveSeconds != 45 {
		t.Fatalf("trend aggregation is wrong: %+v", report.Trends)
	}
	if len(report.Pages) != 2 || report.Pages[0].Sessions != 3 || report.Pages[1].DropOff != 2 || report.Pages[1].Completions != 1 {
		t.Fatalf("page drop-off aggregation is wrong: %+v", report.Pages)
	}
	if got := report.Breakdowns["utm_campaigns"]; len(got) == 0 || got[0].Key != "launch" {
		t.Fatalf("campaign breakdown missing: %+v", got)
	}
	if got := report.Breakdowns["search_terms"]; len(got) != 1 || got[0].Key != "quarterly report" {
		t.Fatalf("search breakdown missing: %+v", got)
	}
	if got := report.Breakdowns["ctas"]; len(got) != 1 || got[0].Key != "hero" {
		t.Fatalf("CTA breakdown missing: %+v", got)
	}
	if len(report.Funnel) != 7 || report.Funnel[0].Events != 3 || report.Funnel[0].Sessions != 3 || report.Funnel[1].Events != 2 || report.Funnel[2].Sessions != 1 {
		t.Fatalf("funnel aggregation is wrong: %+v", report.Funnel)
	}
	if len(report.Funnels) < 2 || len(report.Publications) != 2 || report.Publications[0].Totals.LegacyViews != 7 {
		t.Fatalf("campaign/placement funnels or publications missing: %+v", report)
	}
}

func TestHistoricalViewsDoNotBecomeReaders(t *testing.T) {
	report := BuildReport(ReportInput{
		Range:        Range{From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		Publications: []Publication{{ID: "legacy", Title: "Legacy"}}, LegacyViews: map[string]int64{"legacy": 42},
	})
	if report.Totals.LegacyViews != 42 || report.Totals.Sessions != 0 || report.Totals.UniqueReaders != 0 || report.Totals.Views != 0 {
		t.Fatalf("historical requests were relabeled: %+v", report.Totals)
	}
}

func TestExportPrivacyAndSpreadsheetSafety(t *testing.T) {
	events := []*models.AnalyticsEvent{{
		ID: "event", WorkspaceID: "workspace", FlipbookID: "book", Type: models.AnalyticsShare,
		OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), SessionIDHash: "secret-session",
		ReaderIDHash: "secret-reader", SearchTerms: "=formula", Metadata: map[string]string{"channel": "email", "unknown": "secret"},
	}}
	var output bytes.Buffer
	if err := WriteEventsCSV(&output, events); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Contains(text, "secret-session") || strings.Contains(text, "secret-reader") || strings.Contains(text, "unknown") {
		t.Fatalf("private fields leaked into CSV: %s", text)
	}
	if !strings.Contains(text, "'=formula") {
		t.Fatalf("spreadsheet formula was not neutralized: %s", text)
	}
}

type sliceEventLister struct{ events []*models.AnalyticsEvent }

func (s sliceEventLister) ListAnalyticsEvents(_ context.Context, filter models.AnalyticsFilter, limit int64) ([]*models.AnalyticsEvent, error) {
	result := make([]*models.AnalyticsEvent, 0)
	for _, event := range s.events {
		if !event.OccurredAt.Before(filter.From) && event.OccurredAt.Before(filter.To) {
			result = append(result, event)
		}
	}
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result, nil
}

func TestLoadEventsSplitsFullPages(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := make([]*models.AnalyticsEvent, 0, 10001)
	for index := 0; index < 10001; index++ {
		occurred := start
		if index >= 5000 {
			occurred = start.Add(time.Hour)
		}
		events = append(events, &models.AnalyticsEvent{ID: string(rune(index + 1)), OccurredAt: occurred})
	}
	loaded, err := LoadEvents(context.Background(), sliceEventLister{events: events}, models.AnalyticsFilter{From: start, To: start.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(events) {
		t.Fatalf("loaded %d of %d events", len(loaded), len(events))
	}
}
