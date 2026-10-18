package handlers

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/integrations"
	"github.com/jonradoff/flipbook/internal/models"
)

func TestIntegrationRouteContracts(t *testing.T) {
	router := chi.NewRouter()
	NewIntegrationsHandler(nil, nil).RegisterRoutes(router)
	seen := make(map[string]bool)
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"GET /admin/integrations",
		"GET /admin/integrations/{provider}/connect",
		"GET /admin/integrations/{provider}/callback",
		"POST /admin/integrations/{provider}/verify",
		"POST /admin/integrations/{provider}/disconnect",
		"POST /admin/integrations/{provider}/export",
		"GET /admin/integrations/{provider}/statistics",
		"POST /admin/integrations/webhooks",
		"POST /admin/integrations/webhooks/test",
		"POST /admin/integrations/webhooks/{id}",
		"GET /admin/integrations/webhooks/{id}/deliveries",
	} {
		if !seen[route] {
			t.Errorf("missing integration route %s", route)
		}
	}
}

func TestIntegrationErrorDetails(t *testing.T) {
	for _, test := range []struct {
		err     error
		status  int
		code    string
		message string
	}{
		{auth.ErrUnauthorized, http.StatusUnauthorized, "unauthorized", "Sign in"},
		{auth.ErrForbidden, http.StatusForbidden, "forbidden", "permission"},
		{integrations.ErrNotConfigured, http.StatusServiceUnavailable, "not_configured", "not configured"},
		{integrations.ErrPermission, http.StatusForbidden, "permission_denied", "app approval"},
		{errors.New("unexpected"), http.StatusInternalServerError, "internal_error", "could not be completed"},
	} {
		status, code, message := integrationErrorDetails(test.err)
		if status != test.status || code != test.code || !strings.Contains(message, test.message) {
			t.Errorf("integrationErrorDetails(%v) = %d, %s, %q", test.err, status, code, message)
		}
	}
}

func TestIntegrationRedirectContainsOnlyNoticeAndWarning(t *testing.T) {
	destination := integrationRedirectURL("Connection verified", "Revocation failed")
	parsed, err := url.Parse(destination)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/admin/integrations" || parsed.Host != "" || parsed.Query().Get("notice") != "Connection verified" || parsed.Query().Get("warning") != "Revocation failed" {
		t.Fatalf("unexpected integration redirect: %q", destination)
	}
}

func TestAdminIntegrationsTemplateRendersAccessibleWorkflows(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate integration template test file")
	}
	templatePath := filepath.Join(filepath.Dir(filename), "..", "..", "web", "templates", "admin", "integrations.html")
	tmpl, err := template.New("integrations-test").ParseFiles(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := &integrationsPageData{
		CSRFToken: "csrf-test", CanManageWebhook: true,
		Notice: "Connection verified", Warning: "Provider revocation failed", WebhookSecret: "one-time-secret",
		Providers: []integrationProviderView{
			{
				Info:       integrations.ProviderInfo{Name: integrations.ProviderCanva, DisplayName: "Canva", Configured: true, SupportsStatistics: true, ApprovalNote: "App approval required"},
				Connection: &models.IntegrationConnection{Provider: integrations.ProviderCanva, Status: models.IntegrationConnected, DisplayName: "Design team"},
				Available:  true, Connected: true, StatusLabel: "connected", LastVerified: "2026-01-02 03:04 UTC",
			},
			{
				Info:        integrations.ProviderInfo{Name: integrations.ProviderAdobeExpress, DisplayName: "Adobe Express", Configured: false, ConfigurationMessage: "OAuth is disabled", SupportsStatistics: true},
				StatusLabel: "Not connected",
			},
		},
		Webhooks:       []*models.WebhookSubscription{{ID: "subscription-1", URL: "https://example.com/hooks", Events: []string{"*"}, Enabled: true}},
		Deliveries:     []*models.WebhookDelivery{{EventType: "integration.test", Status: models.WebhookDelivered, Attempts: 1, MaxAttempts: 8, ResponseStatus: 204, CreatedAt: now}},
		StatisticsJSON: `{"title":"<script>alert(1)</script>"}`,
	}
	var output bytes.Buffer
	if err := tmpl.ExecuteTemplate(&output, "admin_integrations", data); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		`aria-current="page"`, `role="status"`, `role="alert"`,
		`for="export-id-canva"`, `id="export-id-canva"`, `name="external_id"`,
		`for="statistics-id-canva"`, `for="webhook-url"`, `for="webhook-events"`,
		`name="csrf_token" value="csrf-test"`, `Copy this secret now`, `one-time-secret`,
		`X-Flipbook-Signature`, `App approval required`, `Connection unavailable`,
		`scope="col"`, `&lt;script&gt;alert(1)&lt;/script&gt;`,
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("rendered integration page is missing %q", expected)
		}
	}
	if strings.Contains(html, `<script>alert(1)</script>`) {
		t.Fatal("provider statistics were rendered as executable markup")
	}
	if strings.Contains(html, "access_token_encrypted") || strings.Contains(html, "refresh_token_encrypted") {
		t.Fatal("provider token storage fields appeared in the admin page")
	}
}
