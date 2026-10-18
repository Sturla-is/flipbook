package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/auth"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/sales"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type salesHandlerStore struct {
	fb           *models.Flipbook
	purchases    map[string]*models.Purchase
	entitlements map[string]*models.Entitlement
}

func (s *salesHandlerStore) GetFlipbookInWorkspace(_ context.Context, workspaceID, id string) (*models.Flipbook, error) {
	if s.fb == nil || s.fb.WorkspaceID != workspaceID || s.fb.ID != id {
		return nil, mongo.ErrNoDocuments
	}
	clone := *s.fb
	return &clone, nil
}
func (s *salesHandlerStore) UpdateFlipbookMetadata(_ context.Context, fb *models.Flipbook) error {
	s.fb = fb
	return nil
}
func (s *salesHandlerStore) CreatePurchase(_ context.Context, purchase *models.Purchase) error {
	s.purchases[purchase.ID] = purchase
	return nil
}
func (s *salesHandlerStore) GetPurchase(_ context.Context, workspaceID, id string) (*models.Purchase, error) {
	purchase := s.purchases[id]
	if purchase == nil || (workspaceID != "" && purchase.WorkspaceID != workspaceID) {
		return nil, mongo.ErrNoDocuments
	}
	return purchase, nil
}
func (s *salesHandlerStore) GetPurchaseByCheckoutSession(_ context.Context, id string) (*models.Purchase, error) {
	for _, purchase := range s.purchases {
		if purchase.StripeCheckoutSessionID == id {
			return purchase, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}
func (s *salesHandlerStore) MarkPurchasePaid(_ context.Context, id, eventID string, entitlement *models.Entitlement) error {
	purchase := s.purchases[id]
	now := time.Now().UTC()
	purchase.Status = models.PurchasePaid
	purchase.PurchasedAt = &now
	purchase.StripeEventID = eventID
	entitlement.ID = "entitlement-" + id
	entitlement.WorkspaceID, entitlement.FlipbookID, entitlement.PurchaseID = purchase.WorkspaceID, purchase.FlipbookID, id
	entitlement.EmailHash = purchase.EmailHash
	entitlement.Status = models.EntitlementActive
	entitlement.GrantedAt = now
	s.entitlements[id] = entitlement
	return nil
}
func (s *salesHandlerStore) RefundPurchase(_ context.Context, workspaceID, id, eventID, reason string) error {
	purchase := s.purchases[id]
	purchase.Status = models.PurchaseRefunded
	if entitlement := s.entitlements[id]; entitlement != nil {
		now := time.Now().UTC()
		entitlement.Status = models.EntitlementRevoked
		entitlement.RevokedAt = &now
	}
	return nil
}
func (s *salesHandlerStore) ListPurchases(_ context.Context, filter database.PurchaseFilter) ([]*models.Purchase, error) {
	result := []*models.Purchase{}
	for _, purchase := range s.purchases {
		if purchase.WorkspaceID == filter.WorkspaceID {
			result = append(result, purchase)
		}
	}
	return result, nil
}
func (s *salesHandlerStore) HasEntitlement(_ context.Context, workspaceID, flipbookID, userID, emailHash string, now time.Time) (bool, error) {
	for _, entitlement := range s.entitlements {
		if entitlement.WorkspaceID == workspaceID && entitlement.FlipbookID == flipbookID && entitlement.EmailHash == emailHash {
			return entitlement.Active(now), nil
		}
	}
	return false, nil
}
func (s *salesHandlerStore) GetEntitlementByPurchase(_ context.Context, id string) (*models.Entitlement, error) {
	if s.entitlements[id] == nil {
		return nil, mongo.ErrNoDocuments
	}
	return s.entitlements[id], nil
}
func (s *salesHandlerStore) RevokeEntitlement(_ context.Context, workspaceID, id, reason string) error {
	for _, entitlement := range s.entitlements {
		if entitlement.ID == id {
			entitlement.Status = models.EntitlementRevoked
			return nil
		}
	}
	return mongo.ErrNoDocuments
}

type salesHandlerProvider struct {
	sessions map[string]*sales.CheckoutSession
}

func (p *salesHandlerProvider) CreateCheckoutSession(_ context.Context, input sales.CheckoutSessionInput) (*sales.CheckoutSession, error) {
	session := &sales.CheckoutSession{ID: "cs_" + input.PurchaseID, URL: "https://checkout.example.test/" + input.PurchaseID}
	p.sessions[session.ID] = session
	return session, nil
}
func (p *salesHandlerProvider) RetrieveCheckoutSession(_ context.Context, id string) (*sales.CheckoutSession, error) {
	if p.sessions[id] == nil {
		return nil, errors.New("missing session")
	}
	return p.sessions[id], nil
}
func (p *salesHandlerProvider) ExpireCheckoutSession(context.Context, string) error { return nil }
func (p *salesHandlerProvider) CreateRefund(context.Context, sales.RefundInput) (*sales.Refund, error) {
	return &sales.Refund{Status: "succeeded"}, nil
}

func newSalesHandlerFixture(t *testing.T) (*SalesHandler, *salesHandlerStore, *salesHandlerProvider) {
	t.Helper()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	fb := &models.Flipbook{
		ID: "book", WorkspaceID: models.DefaultWorkspaceID, Title: "Paid publication", Slug: "paid-publication",
		Status: models.StatusReady, PublicationState: models.PublicationPublished, Visibility: models.VisibilityPublic,
		PageCount: 3, Sales: models.SalesSettings{Enabled: true, PriceCents: 1250, Currency: "USD", PreviewPages: 1},
	}
	fb.ApplyDefaults()
	store := &salesHandlerStore{fb: fb, purchases: make(map[string]*models.Purchase), entitlements: make(map[string]*models.Entitlement)}
	provider := &salesHandlerProvider{sessions: make(map[string]*sales.CheckoutSession)}
	service, err := sales.NewService(store, provider, sales.Options{
		StripeEnabled: true, WebhookSecret: "whsec_test", SigningSecret: strings.Repeat("secret", 8),
		BaseURL: "https://flipbook.example.test", CookieSecure: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.ParseFiles(
		"../../web/templates/sales/checkout.html", "../../web/templates/sales/success.html",
		"../../web/templates/sales/cancel.html", "../../web/templates/sales/error.html",
		"../../web/templates/admin/sales.html", "../../web/templates/admin/sales_settings.html",
	)
	if err != nil {
		t.Fatal(err)
	}
	return NewSalesHandler(service, tmpl, "https://flipbook.example.test"), store, provider
}

func TestPublicCheckoutContractAndSuccessFlow(t *testing.T) {
	handler, store, provider := newSalesHandlerFixture(t)
	router := chi.NewRouter()
	router.Post("/api/public/flipbooks/{id}/checkout", handler.Checkout)
	router.Get("/sales/success", handler.CheckoutSuccess)

	request := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/checkout", strings.NewReader(`{"email":"reader@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("checkout status = %d: %s", response.Code, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["checkout_url"] == "" {
		t.Fatalf("checkout contract = %+v, %v", body, err)
	}
	if len(body) != 1 || len(store.purchases) != 1 {
		t.Fatalf("unexpected checkout response or persistence: %+v, %+v", body, store.purchases)
	}
	var purchase *models.Purchase
	for _, candidate := range store.purchases {
		purchase = candidate
	}
	provider.sessions[purchase.StripeCheckoutSessionID] = &sales.CheckoutSession{
		ID: purchase.StripeCheckoutSessionID, ClientReferenceID: purchase.ID, PaymentStatus: "paid",
		AmountTotal: purchase.AmountCents, Currency: purchase.Currency, Metadata: map[string]string{
			"purchase_id": purchase.ID, "workspace_id": purchase.WorkspaceID, "flipbook_id": purchase.FlipbookID,
		},
	}
	success := httptest.NewRecorder()
	router.ServeHTTP(success, httptest.NewRequest(http.MethodGet, "/sales/success?session_id="+purchase.StripeCheckoutSessionID, nil))
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), "Paid publication") {
		t.Fatalf("success response = %d: %s", success.Code, success.Body.String())
	}
	cookies := success.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("success cookies = %+v", cookies)
	}
	readerRequest := httptest.NewRequest(http.MethodGet, "/v/paid-publication", nil)
	readerRequest.AddCookie(cookies[0])
	reader, err := handler.PurchaserReader(readerRequest, purchase.WorkspaceID, purchase.FlipbookID)
	if err != nil || reader.EmailHash != purchase.EmailHash {
		t.Fatalf("success cookie reader = %+v, %v", reader, err)
	}
}

func TestCheckoutFormFallbackAndWebhookSignatureFailure(t *testing.T) {
	handler, _, _ := newSalesHandlerFixture(t)
	form := url.Values{"email": {"reader@example.com"}}
	request := httptest.NewRequest(http.MethodPost, "/api/public/flipbooks/book/checkout", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
	routeContext := chi.RouteContext(request.Context())
	routeContext.URLParams.Add("id", "book")
	response := httptest.NewRecorder()
	handler.Checkout(response, request)
	if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "https://checkout.example.test/") {
		t.Fatalf("form checkout = %d, %q", response.Code, response.Header().Get("Location"))
	}
	webhook := httptest.NewRecorder()
	handler.StripeWebhook(webhook, httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", strings.NewReader(`{"id":"evt"}`)))
	if webhook.Code != http.StatusBadRequest {
		t.Fatalf("unsigned webhook status = %d", webhook.Code)
	}
}

func TestSalesAdminAuthenticationAndInputHelpers(t *testing.T) {
	handler, _, _ := newSalesHandlerFixture(t)
	for name, call := range map[string]http.HandlerFunc{
		"report": handler.AdminSales, "orders CSV": handler.AdminSalesCSV,
		"customers CSV": handler.AdminCustomersCSV, "settings": handler.AdminSalesSettings,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			call(response, httptest.NewRequest(http.MethodGet, "/admin/sales", nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status = %d", response.Code)
			}
		})
	}
	if got := csvSafe("=HYPERLINK(\"https://invalid\")"); !strings.HasPrefix(got, "'=") {
		t.Fatalf("CSV formula was not neutralized: %q", got)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/flipbooks/book/sales", strings.NewReader(`{"enabled":true,"price_cents":999,"currency":"eur","preview_pages":1}`))
	request.Header.Set("Content-Type", "application/json")
	settings, isForm, err := salesSettingsFromRequest(httptest.NewRecorder(), request)
	if err != nil || isForm || settings.PriceCents != 999 || settings.Currency != "eur" {
		t.Fatalf("JSON settings = %+v, %v, %v", settings, isForm, err)
	}
}

type salesHandlerAuthStore struct {
	auth.Store
	session    *models.Session
	user       *models.User
	membership *models.Membership
}

func (s *salesHandlerAuthStore) GetSessionByToken(_ context.Context, token string) (*models.Session, error) {
	if models.HashToken(token) != s.session.TokenHash {
		return nil, mongo.ErrNoDocuments
	}
	return s.session, nil
}
func (s *salesHandlerAuthStore) GetUser(_ context.Context, id string) (*models.User, error) {
	if s.user.ID != id {
		return nil, mongo.ErrNoDocuments
	}
	return s.user, nil
}
func (s *salesHandlerAuthStore) GetMembership(_ context.Context, workspaceID, userID string) (*models.Membership, error) {
	if s.membership.WorkspaceID != workspaceID || s.membership.UserID != userID {
		return nil, mongo.ErrNoDocuments
	}
	return s.membership, nil
}
func (s *salesHandlerAuthStore) RecordAuditEvent(context.Context, *models.AuditEvent) error {
	return nil
}

func salesHandlerAuthentication(t *testing.T, role models.Role) (func(http.Handler) http.Handler, *http.Cookie) {
	t.Helper()
	now := time.Now().UTC()
	rawToken := "test-session-token"
	store := &salesHandlerAuthStore{
		session: &models.Session{
			TokenHash: models.HashToken(rawToken), UserID: "admin", WorkspaceID: models.DefaultWorkspaceID,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: &now,
		},
		user:       &models.User{ID: "admin", Email: "admin@example.com", Name: "Admin"},
		membership: &models.Membership{ID: "membership", WorkspaceID: models.DefaultWorkspaceID, UserID: "admin", Role: role},
	}
	identity, err := auth.NewWithOptions(store, nil, auth.Options{SessionSecret: "test-session-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return identity.RequireAuth, &http.Cookie{Name: auth.SessionCookieName, Value: rawToken, Path: "/"}
}

func TestAdminReportsAndCSVWithAuthenticatedAdmin(t *testing.T) {
	handler, store, _ := newSalesHandlerFixture(t)
	now := time.Now().UTC()
	store.purchases["order-csv"] = &models.Purchase{
		ID: "order-csv", WorkspaceID: models.DefaultWorkspaceID, FlipbookID: "book", Email: "reader@example.com",
		AmountCents: 1250, Currency: "USD", Status: models.PurchasePaid, CreatedAt: now, PurchasedAt: &now,
	}
	authenticate, cookie := salesHandlerAuthentication(t, models.RoleAdmin)
	for _, test := range []struct {
		path, handlerName, expected string
		handler                     http.HandlerFunc
	}{
		{path: "/admin/sales", handlerName: "HTML", expected: "Sales and customers", handler: handler.AdminSales},
		{path: "/admin/sales.csv", handlerName: "orders CSV", expected: "order-csv", handler: handler.AdminSalesCSV},
		{path: "/admin/sales/customers.csv", handlerName: "customers CSV", expected: "reader@example.com", handler: handler.AdminCustomersCSV},
	} {
		t.Run(test.handlerName, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			authenticate(test.handler).ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.expected) {
				t.Fatalf("admin response = %d: %s", response.Code, response.Body.String())
			}
		})
	}
	readerAuth, readerCookie := salesHandlerAuthentication(t, models.RoleReader)
	request := httptest.NewRequest(http.MethodGet, "/admin/sales", nil)
	request.AddCookie(readerCookie)
	response := httptest.NewRecorder()
	readerAuth(http.HandlerFunc(handler.AdminSales)).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reader role received admin report: %d", response.Code)
	}
}
