package models

import (
	"net/url"
	"testing"
	"time"
)

func TestAnalyticsPrivacyPreparation(t *testing.T) {
	occurred := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	event := AnalyticsEvent{
		Type: AnalyticsQualifiedRead, OccurredAt: occurred,
		SessionIDHash: "session", ReaderIDHash: "reader", Country: "us",
		Referrer: "https://user:password@Example.COM/path?utm_source=secret&token=private#fragment",
	}
	event.PrepareForStorage(30*24*time.Hour, "required", false)
	if event.SessionIDHash != "" || event.ReaderIDHash != "" {
		t.Fatal("identifiers stored without required consent")
	}
	if event.Referrer != "https://example.com/path" {
		t.Fatalf("referrer was not sanitized: %q", event.Referrer)
	}
	if event.Country != "US" || !event.ExpiresAt.Equal(occurred.Add(30*24*time.Hour)) {
		t.Fatalf("country or retention not normalized: %+v", event)
	}
	event.SessionIDHash = "session"
	event.ReaderIDHash = "reader"
	event.PrepareForStorage(time.Hour, "anonymous", false)
	if event.SessionIDHash == "" || event.ReaderIDHash == "" {
		t.Fatal("anonymous consent policy should retain pseudonymous identifiers")
	}
}

func TestReferrerUTMDeviceAndBotNormalization(t *testing.T) {
	for _, invalid := range []string{"javascript:alert(1)", "not a referrer", "//example.com/path"} {
		if got := NormalizeReferrer(invalid); got != "" {
			t.Fatalf("expected invalid referrer %q to be dropped, got %q", invalid, got)
		}
	}
	if got := NormalizeReferrer("/previous?token=secret"); got != "/previous" {
		t.Fatalf("relative referrer not sanitized: %q", got)
	}
	campaign := CampaignFromQuery(url.Values{"utm_source": {"Newsletter"}, "utm_campaign": {"Launch"}, "other": {"drop"}})
	if campaign.Source != "newsletter" || campaign.Campaign != "launch" {
		t.Fatalf("unexpected campaign: %+v", campaign)
	}
	device, browser := CoarseDeviceBrowser("Mozilla/5.0 (iPhone) AppleWebKit Chrome/120 Mobile Safari/601")
	if device != "mobile" || browser != "chrome" {
		t.Fatalf("unexpected coarse identity %s/%s", device, browser)
	}
	if !IsObviousBot("Googlebot/2.1") || IsObviousBot("Mozilla/5.0 Chrome/120") {
		t.Fatal("bot classification is incorrect")
	}
}
