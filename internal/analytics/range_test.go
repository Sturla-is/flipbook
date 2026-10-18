package analytics

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	r := httptest.NewRequest("GET", "/analytics?from=2026-03-01&to=2026-03-03", nil)
	parsed, err := ParseRange(r, now)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.From.Format("2006-01-02") != "2026-03-01" || parsed.To.Format("2006-01-02") != "2026-03-04" {
		t.Fatalf("inclusive dates not normalized: %+v", parsed)
	}
	from, to := parsed.QueryDates()
	if from != "2026-03-01" || to != "2026-03-03" {
		t.Fatalf("query dates = %s/%s", from, to)
	}

	r = httptest.NewRequest("GET", "/analytics", nil)
	parsed, err = ParseRange(r, now)
	if err != nil || parsed.To.Sub(parsed.From) != 30*24*time.Hour {
		t.Fatalf("default range = %+v, %v", parsed, err)
	}

	r = httptest.NewRequest("GET", "/analytics?from=2024-01-01&to=2026-01-01", nil)
	if _, err = ParseRange(r, now); !errors.Is(err, ErrRangeTooLarge) {
		t.Fatalf("expected range limit, got %v", err)
	}
}
