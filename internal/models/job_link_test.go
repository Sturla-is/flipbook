package models

import (
	"testing"
	"time"
)

func TestRetryDelayAndLeaseExpiry(t *testing.T) {
	if got := RetryDelay(1, time.Second, time.Hour); got != time.Second {
		t.Fatalf("first retry = %s", got)
	}
	if got := RetryDelay(4, time.Second, time.Hour); got != 8*time.Second {
		t.Fatalf("exponential retry = %s", got)
	}
	if got := RetryDelay(100, time.Second, 5*time.Second); got != 5*time.Second {
		t.Fatalf("capped retry = %s", got)
	}
	now := time.Now()
	leaseEnds := now.Add(-time.Second)
	if !(Job{Status: JobRunning, LeaseExpiresAt: &leaseEnds}).LeaseExpired(now) {
		t.Fatal("expired lease not detected")
	}
}

func TestLinkOverlayValidation(t *testing.T) {
	valid := LinkOverlay{
		FlipbookID: "book", Page: 1, X: 0.1, Y: 0.1, Width: 0.4, Height: 0.2,
		Type: LinkExternal, URL: "https://example.com/path",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []LinkOverlay{
		valid,
		valid,
		valid,
		valid,
	}
	tests[0].URL = "javascript:alert(1)"
	tests[1].Width = 0.95
	tests[2].Type = LinkInternal
	tests[2].TargetPage = 0
	tests[3].Type = "unknown"
	for i, test := range tests {
		if err := test.Validate(); err == nil {
			t.Fatalf("invalid link %d passed validation", i)
		}
	}
	internal := valid
	internal.Type = LinkInternal
	internal.TargetPage = 2
	internal.URL = ""
	if err := internal.Validate(); err != nil {
		t.Fatalf("valid internal link rejected: %v", err)
	}
}
