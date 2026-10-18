package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/analytics"
	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type fakeAnalyticsStore struct {
	flipbook  *models.Flipbook
	workspace *models.Workspace
	events    []*models.AnalyticsEvent
	writes    []database.AnalyticsWriteOptions
}

func (f *fakeAnalyticsStore) GetFlipbookContext(_ context.Context, id string) (*models.Flipbook, error) {
	if f.flipbook == nil || f.flipbook.ID != id {
		return nil, mongo.ErrNoDocuments
	}
	return f.flipbook, nil
}

func (f *fakeAnalyticsStore) GetFlipbookInWorkspace(ctx context.Context, workspaceID, id string) (*models.Flipbook, error) {
	if f.flipbook == nil || f.flipbook.WorkspaceID != workspaceID {
		return nil, mongo.ErrNoDocuments
	}
	return f.GetFlipbookContext(ctx, id)
}

func (f *fakeAnalyticsStore) GetWorkspace(_ context.Context, id string) (*models.Workspace, error) {
	if f.workspace == nil || f.workspace.ID != id {
		return nil, mongo.ErrNoDocuments
	}
	return f.workspace, nil
}

func (f *fakeAnalyticsStore) UpdateWorkspaceSettings(_ context.Context, workspace *models.Workspace) error {
	f.workspace = workspace
	return nil
}

func (f *fakeAnalyticsStore) SearchFlipbooks(_ context.Context, _ database.FlipbookFilter) ([]*models.Flipbook, int64, error) {
	if f.flipbook == nil {
		return nil, 0, nil
	}
	return []*models.Flipbook{f.flipbook}, 1, nil
}

func (f *fakeAnalyticsStore) GetMembership(_ context.Context, _, _ string) (*models.Membership, error) {
	return nil, mongo.ErrNoDocuments
}

func (f *fakeAnalyticsStore) ListAnalyticsEvents(_ context.Context, filter models.AnalyticsFilter, limit int64) ([]*models.AnalyticsEvent, error) {
	result := make([]*models.AnalyticsEvent, 0)
	for _, event := range f.events {
		if event.WorkspaceID != filter.WorkspaceID || event.OccurredAt.Before(filter.From) || !event.OccurredAt.Before(filter.To) {
			continue
		}
		result = append(result, event)
	}
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *fakeAnalyticsStore) InsertAnalyticsEvent(_ context.Context, event *models.AnalyticsEvent, options database.AnalyticsWriteOptions) error {
	for _, existing := range f.events {
		if event.ID != "" && existing.ID == event.ID {
			return mongo.WriteError{Code: 11000, Message: "duplicate key"}
		}
	}
	f.events = append(f.events, event)
	f.writes = append(f.writes, options)
	return nil
}

func (f *fakeAnalyticsStore) GetViewCount(string) int { return 7 }

func newFakeAnalyticsHandler(policy string) (*AnalyticsHandler, *fakeAnalyticsStore) {
	store := &fakeAnalyticsStore{
		flipbook: &models.Flipbook{
			ID: "book", WorkspaceID: "workspace", OwnerID: "owner", Title: "Publication", Slug: "publication",
			Status: models.StatusReady, PageCount: 3, PublicationState: models.PublicationPublished, Visibility: models.VisibilityPublic,
		},
		workspace: &models.Workspace{ID: "workspace", Settings: models.WorkspaceSettings{
			AnalyticsConsentPolicy: policy, AnalyticsRetentionDays: 30,
		}},
	}
	cfg := &config.Config{Analytics: config.AnalyticsConfig{
		Enabled: true, RetentionDays: 90, ConsentPolicy: policy, AllowCountry: true, ReaderIDPepper: "pepper", IgnoreAdminPreviews: true,
	}}
	handler := NewAnalyticsHandler(store, nil, nil, cfg)
	handler.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return handler, store
}

func analyticsRouter(handler *AnalyticsHandler) http.Handler {
	router := chi.NewRouter()
	router.Post("/api/public/flipbooks/{id}/events", handler.Events)
	router.Post("/api/public/flipbooks/{id}/analytics/consent", handler.UpdateConsent)
	router.Get("/admin/analytics/report", handler.Report)
	return router
}

func TestPublicEventIngestionContractAndPrivacy(t *testing.T) {
	handler, store := newFakeAnalyticsHandler(analytics.ConsentAnonymous)
	body := `{"events":[{"type":"page_view","page":1,"duration_ms":1500,"session_id":"session-12345","metadata":{"event_id":"event-123456","utm_medium":"email"}},{"type":"search","session_id":"session-12345","metadata":{"query":"Quarterly Report","unknown":"discard"}}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/events?utm_source=newsletter", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
	r.Header.Set("Referer", "https://referrer.example/read?utm_campaign=launch&secret=private")
	r.Header.Set("CF-IPCountry", "us")
	r.RemoteAddr = "192.0.2.10:12345"
	w := httptest.NewRecorder()
	analyticsRouter(handler).ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(store.events) != 2 {
		t.Fatalf("stored %d events", len(store.events))
	}
	first := store.events[0]
	if first.Type != models.AnalyticsPageExposure || first.ActiveSeconds != 1 || first.SessionIDHash == "" || first.ReaderIDHash == "" {
		t.Fatalf("unexpected event: %+v", first)
	}
	if first.Referrer != "https://referrer.example/read" || first.UTM.Source != "newsletter" || first.UTM.Medium != "email" || first.UTM.Campaign != "launch" {
		t.Fatalf("context was not enriched safely: %+v", first)
	}
	if first.Country != "US" || first.Metadata["unknown"] != "" || !first.ExpiresAt.Equal(handler.now().Add(30*24*time.Hour)) {
		t.Fatalf("privacy/retention failed: %+v", first)
	}
	if store.events[1].SearchTerms != "quarterly report" {
		t.Fatalf("search term was not normalized: %+v", store.events[1])
	}
	serialized, _ := json.Marshal(store.events)
	for _, private := range []string{"192.0.2.10", "secret=private", "session-12345", "discard"} {
		if strings.Contains(string(serialized), private) {
			t.Fatalf("private value %q was stored: %s", private, serialized)
		}
	}
}

func TestPublicEventConsentOptOutAndFiltering(t *testing.T) {
	t.Run("required consent strips identifiers", func(t *testing.T) {
		handler, store := newFakeAnalyticsHandler(analytics.ConsentRequired)
		r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/events", strings.NewReader(`{"events":[{"type":"view","session_id":"session-12345"}]}`))
		r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
		w := httptest.NewRecorder()
		analyticsRouter(handler).ServeHTTP(w, r)
		if w.Code != http.StatusAccepted || len(store.events) != 1 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		if store.events[0].SessionIDHash != "" || store.events[0].ReaderIDHash != "" {
			t.Fatal("identifiers stored without consent")
		}
	})
	t.Run("opt out suppresses collection", func(t *testing.T) {
		handler, store := newFakeAnalyticsHandler(analytics.ConsentAnonymous)
		r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/events", strings.NewReader(`{"events":[{"type":"view"}]}`))
		r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
		r.AddCookie(&http.Cookie{Name: analytics.ConsentCookieName, Value: "denied"})
		w := httptest.NewRecorder()
		analyticsRouter(handler).ServeHTTP(w, r)
		if w.Code != http.StatusOK || len(store.events) != 0 || !strings.Contains(w.Body.String(), `"accepted":0`) {
			t.Fatalf("opt-out response = %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("bot is filtered", func(t *testing.T) {
		handler, store := newFakeAnalyticsHandler(analytics.ConsentAnonymous)
		r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/events", strings.NewReader(`{"events":[{"type":"view"}]}`))
		r.Header.Set("User-Agent", "Googlebot/2.1")
		w := httptest.NewRecorder()
		analyticsRouter(handler).ServeHTTP(w, r)
		if w.Code != http.StatusOK || len(store.events) != 0 {
			t.Fatalf("bot response = %d %s", w.Code, w.Body.String())
		}
	})
}

func TestPublicEventAccessAndValidation(t *testing.T) {
	handler, store := newFakeAnalyticsHandler(analytics.ConsentAnonymous)
	for name, body := range map[string]string{
		"server-only lead":  `{"events":[{"type":"lead"}]}`,
		"disabled download": `{"events":[{"type":"download"}]}`,
		"invalid page":      `{"events":[{"type":"page_view","page":99}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/events", strings.NewReader(body))
			r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
			w := httptest.NewRecorder()
			analyticsRouter(handler).ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest && w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
		})
	}
	if len(store.events) != 0 {
		t.Fatal("invalid batches stored events")
	}
}

func TestConsentResponseReflectsNewState(t *testing.T) {
	handler, _ := newFakeAnalyticsHandler(analytics.ConsentRequired)
	r := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/analytics/consent", strings.NewReader(`{"consent":false}`))
	r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
	w := httptest.NewRecorder()
	analyticsRouter(handler).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"consent":"denied"`) || !strings.Contains(w.Body.String(), `"tracking":false`) {
		t.Fatalf("consent response = %d %s", w.Code, w.Body.String())
	}
}

func TestAnalyticsReportRequiresAuthentication(t *testing.T) {
	handler, _ := newFakeAnalyticsHandler(analytics.ConsentAnonymous)
	r := httptest.NewRequest(http.MethodGet, "/admin/analytics/report", nil)
	w := httptest.NewRecorder()
	analyticsRouter(handler).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
