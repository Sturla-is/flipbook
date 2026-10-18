package handlers

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/analytics"
)

func TestAnalyticsTemplateRenders(t *testing.T) {
	path := "../../web/templates/admin/analytics.html"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = "/src/web/templates/admin/analytics.html"
	}
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	data := analyticsPageData{
		Report: analytics.Report{
			GeneratedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Scope:        analytics.Scope{WorkspaceID: "workspace"},
			Totals:       analytics.Totals{Views: 10, CompletionRate: 0.5},
			Publications: []analytics.PublicationMetrics{{Publication: analytics.Publication{ID: "book", Title: "Publication"}}},
			Breakdowns:   map[string][]analytics.Breakdown{"referrers": {{Key: "(direct)", Views: 10}}},
		},
		Publications: []analytics.Publication{{ID: "book", Title: "Publication"}},
		From:         "2026-01-01", To: "2026-01-02", ConsentPolicy: analytics.ConsentRequired,
		RetentionDays: 30, CanManage: true, Breakdowns: []analyticsBreakdownTable{{Title: "Referrers", Dimension: "referrers", Items: []analytics.Breakdown{{Key: "(direct)"}}}},
	}
	var output bytes.Buffer
	if err := tmpl.ExecuteTemplate(&output, "admin_analytics", data); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Report JSON", "Privacy-safe event exports", "/admin/analytics/privacy", `data-breakdown="referrers"`, "Publication"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("template does not contain %q", expected)
		}
	}
}
