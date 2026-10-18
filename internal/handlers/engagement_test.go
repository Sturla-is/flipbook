package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonradoff/flipbook/internal/engagement"
)

func TestDecodeJSONBodyContract(t *testing.T) {
	for name, test := range map[string]struct {
		body    string
		wantErr bool
	}{
		"valid":          {body: `{"email":"reader@example.com","consent":true}`},
		"unknown field":  {body: `{"email":"reader@example.com","consent":true,"role":"admin"}`, wantErr: true},
		"trailing value": {body: `{"email":"reader@example.com","consent":true} {}`, wantErr: true},
		"invalid JSON":   {body: `{"email":`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/lead", strings.NewReader(test.body))
			var input leadSubmission
			err := decodeJSONBody(httptest.NewRecorder(), r, &input)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestSafeReturnURLRejectsExternalRedirects(t *testing.T) {
	for _, value := range []string{"https://evil.example", "//evil.example", "javascript:alert(1)", "relative/path"} {
		if got := safeReturnURL(value, "/v/book"); got != "/v/book" {
			t.Fatalf("safeReturnURL(%q) = %q", value, got)
		}
	}
	if got := safeReturnURL("/v/book?page=2", "/"); got != "/v/book?page=2" {
		t.Fatalf("local return URL was rejected: %q", got)
	}
}

func TestEngagementErrorMapping(t *testing.T) {
	if status, _ := engagementErrorDetails(engagement.ErrConsentRequired); status != http.StatusBadRequest {
		t.Fatalf("consent status = %d", status)
	}
	if status, _ := engagementErrorDetails(engagement.ErrRateLimit); status != http.StatusTooManyRequests {
		t.Fatalf("rate-limit status = %d", status)
	}
	if status, _ := engagementErrorDetails(engagement.ErrNotConfigured); status != http.StatusServiceUnavailable {
		t.Fatalf("configuration status = %d", status)
	}
}

func TestEngagementTemplatesParse(t *testing.T) {
	for _, pattern := range []string{"../../web/templates/engagement/*.html", "../../web/templates/admin/leads*.html"} {
		if _, err := template.New("").ParseGlob(pattern); err != nil {
			t.Fatalf("parse %s: %v", pattern, err)
		}
	}
}
