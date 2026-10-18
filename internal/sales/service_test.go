package sales

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
)

func TestCheckoutCompletionCookieAndPublishingEnforcement(t *testing.T) {
	service, store, provider, fb, now := newSalesFixture(t)
	pending := startTestCheckout(t, service, provider, " Reader@Example.com ")
	if pending.Email != "reader@example.com" || pending.Status != models.PurchasePending {
		t.Fatalf("unexpected pending purchase: %+v", pending)
	}
	if !strings.Contains(provider.lastCreate.SuccessURL, "session_id={CHECKOUT_SESSION_ID}") ||
		!strings.Contains(provider.lastCreate.CancelURL, "purchase_id="+pending.ID) {
		t.Fatalf("return URLs were not bound to the purchase: %+v", provider.lastCreate)
	}
	if provider.lastCreate.AmountCents != 1250 || provider.lastCreate.Currency != "USD" {
		t.Fatalf("incorrect provider amount: %+v", provider.lastCreate)
	}

	purchase := completeTestCheckout(t, service, pending)
	if purchase.Status != models.PurchasePaid || store.entitlements[purchase.ID] == nil {
		t.Fatalf("purchase was not paid and entitled: %+v", purchase)
	}
	cookie, err := service.CookieForPurchase(context.Background(), purchase.WorkspaceID, purchase.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge <= 0 {
		t.Fatalf("insecure purchaser cookie: %+v", cookie)
	}
	request := httptest.NewRequest(http.MethodGet, "https://flipbook.example.test/v/book", nil)
	request.AddCookie(cookie)
	reader, err := service.ReaderFromRequest(context.Background(), request, fb.WorkspaceID, fb.ID)
	if err != nil || reader.EmailHash != purchase.EmailHash {
		t.Fatalf("purchaser reader = %+v, %v", reader, err)
	}
	decision := publishing.EvaluateAccess(publishing.PolicyInput{
		Flipbook: *fb, Evidence: publishing.AccessEvidence{Entitled: reader.EmailHash != ""},
		Resource: publishing.ResourcePage, Page: 5, PageCount: 5, Now: now,
	})
	if !decision.Allowed || !decision.FullAccess {
		t.Fatalf("entitled paid page denied: %+v", decision)
	}
	denied := publishing.EvaluateAccess(publishing.PolicyInput{
		Flipbook: *fb, Resource: publishing.ResourcePage, Page: 5, PageCount: 5, Now: now,
	})
	if denied.Allowed || denied.Status != http.StatusPaymentRequired || denied.Reason != publishing.CodePurchaseRequired {
		t.Fatalf("unentitled paid page allowed: %+v", denied)
	}

	if err := service.Revoke(context.Background(), purchase.WorkspaceID, purchase.ID, "abuse review"); err != nil {
		t.Fatal(err)
	}
	reader, err = service.ReaderFromRequest(context.Background(), request, fb.WorkspaceID, fb.ID)
	if err != nil || reader.EmailHash != "" {
		t.Fatalf("revoked cookie retained access: %+v, %v", reader, err)
	}
}

func TestRepeatPurchasesHaveIndependentEntitlements(t *testing.T) {
	service, store, provider, _, _ := newSalesFixture(t)
	first := completeTestCheckout(t, service, startTestCheckout(t, service, provider, "reader@example.com"))
	second := completeTestCheckout(t, service, startTestCheckout(t, service, provider, "reader@example.com"))
	if first.EmailHash == second.EmailHash {
		t.Fatal("repeat purchases share an entitlement identity")
	}
	if err := service.Refund(context.Background(), first.WorkspaceID, first.ID, "duplicate purchase"); err != nil {
		t.Fatal(err)
	}
	active, err := store.HasEntitlement(context.Background(), second.WorkspaceID, second.FlipbookID, "", second.EmailHash, time.Now())
	if err != nil || !active {
		t.Fatalf("second purchase was affected by first refund: %v, %v", active, err)
	}
	if provider.lastRefund.PaymentIntentID != "pi_"+first.ID || provider.lastRefund.Idempotency != "refund-"+first.ID {
		t.Fatalf("refund was not bound to first payment: %+v", provider.lastRefund)
	}
	calls := provider.refundCalls
	if err := service.Refund(context.Background(), first.WorkspaceID, first.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	if provider.refundCalls != calls {
		t.Fatal("an already reconciled refund created another provider refund")
	}
}

func TestWebhookReplayRefundAndTerminalEventOrdering(t *testing.T) {
	service, store, provider, _, now := newSalesFixture(t)
	pending := startTestCheckout(t, service, provider, "reader@example.com")
	session := provider.sessions[pending.StripeCheckoutSessionID]
	completedPayload := webhookPayload(t, "evt_completed", "checkout.session.completed", session)
	header := signTestWebhook(completedPayload, service.webhookSecret, now)
	if err := service.HandleWebhook(context.Background(), completedPayload, header); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleWebhook(context.Background(), completedPayload, header); err != nil {
		t.Fatalf("replayed completion failed: %v", err)
	}
	if store.purchases[pending.ID].Status != models.PurchasePaid || len(store.entitlements) != 1 {
		t.Fatalf("webhook replay duplicated state: %+v", store.purchases[pending.ID])
	}
	if err := service.HandleWebhook(context.Background(), completedPayload, header+"tampered"); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered webhook error = %v", err)
	}

	partial := chargeRefund{Amount: pending.AmountCents, AmountRefunded: 100, Currency: pending.Currency, Metadata: map[string]string{
		"purchase_id": pending.ID, "workspace_id": pending.WorkspaceID, "flipbook_id": pending.FlipbookID,
	}}
	partialPayload := webhookPayload(t, "evt_partial", "charge.refunded", partial)
	if err := service.HandleWebhook(context.Background(), partialPayload, signTestWebhook(partialPayload, service.webhookSecret, now)); err != nil {
		t.Fatal(err)
	}
	if store.purchases[pending.ID].Status != models.PurchasePaid {
		t.Fatal("partial refund revoked the full purchase")
	}
	full := partial
	full.AmountRefunded = full.Amount
	full.Refunded = true
	refundPayload := webhookPayload(t, "evt_refunded", "charge.refunded", full)
	if err := service.HandleWebhook(context.Background(), refundPayload, signTestWebhook(refundPayload, service.webhookSecret, now)); err != nil {
		t.Fatal(err)
	}
	if store.purchases[pending.ID].Status != models.PurchaseRefunded || store.entitlements[pending.ID].Active(now) {
		t.Fatalf("full refund did not revoke entitlement: %+v", store.entitlements[pending.ID])
	}
	if err := service.HandleWebhook(context.Background(), completedPayload, header); err != nil {
		t.Fatalf("late completion replay failed: %v", err)
	}
	if store.purchases[pending.ID].Status != models.PurchaseRefunded || store.entitlements[pending.ID].Active(now) {
		t.Fatal("late completion reactivated a refunded purchase")
	}
}

func TestCheckoutPersistenceFailureExpiresProviderSession(t *testing.T) {
	service, store, provider, _, _ := newSalesFixture(t)
	store.createErr = errors.New("database unavailable")
	_, err := service.Checkout(context.Background(), CheckoutInput{WorkspaceID: "workspace", FlipbookID: "book", Email: "reader@example.com"})
	if err == nil || len(provider.expired) != 1 {
		t.Fatalf("orphaned provider session was not expired: %v, %+v", err, provider.expired)
	}
}

func TestDisabledSalesAndSettingsValidation(t *testing.T) {
	now := time.Now().UTC()
	store := newFakeSalesStore()
	fb := testSalesPublication(now)
	store.flipbooks[salesKey(fb.WorkspaceID, fb.ID)] = fb
	service, err := NewService(store, nil, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := service.Quote(context.Background(), fb.WorkspaceID, fb.ID)
	if err != nil || quote.Configured {
		t.Fatalf("disabled quote = %+v, %v", quote, err)
	}
	if _, err := service.Checkout(context.Background(), CheckoutInput{WorkspaceID: "workspace", FlipbookID: "book", Email: "reader@example.com"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("disabled checkout error = %v", err)
	}
	if _, err := service.UpdateSettings(context.Background(), fb.WorkspaceID, fb.ID, models.SalesSettings{Enabled: true, PriceCents: 0, Currency: "USD"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero price error = %v", err)
	}
	if _, err := service.UpdateSettings(context.Background(), fb.WorkspaceID, fb.ID, models.SalesSettings{Enabled: true, PriceCents: 100, Currency: "USD", PreviewPages: 6}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("preview bounds error = %v", err)
	}
	updated, err := service.UpdateSettings(context.Background(), fb.WorkspaceID, fb.ID, models.SalesSettings{Enabled: true, PriceCents: 999, Currency: "eur", PreviewPages: 1})
	if err != nil || updated.Sales.Currency != "EUR" {
		t.Fatalf("valid settings failed: %+v, %v", updated, err)
	}
}

func TestReportSeparatesCurrenciesAndAggregatesCustomers(t *testing.T) {
	service, store, _, fb, now := newSalesFixture(t)
	for _, purchase := range []*models.Purchase{
		{ID: "paid-usd", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: "reader@example.com", AmountCents: 1000, Currency: "USD", Status: models.PurchasePaid, CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "refunded-usd", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: "reader@example.com", AmountCents: 500, Currency: "USD", Status: models.PurchaseRefunded, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "paid-eur", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: "other@example.com", AmountCents: 800, Currency: "EUR", Status: models.PurchasePaid, CreatedAt: now.Add(-time.Hour)},
		{ID: "pending", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: "pending@example.com", AmountCents: 900, Currency: "USD", Status: models.PurchasePending, CreatedAt: now},
	} {
		store.purchases[purchase.ID] = purchase
	}
	report, err := service.Report(context.Background(), ReportFilter{WorkspaceID: fb.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orders) != 4 || len(report.Totals) != 2 || len(report.Customers) != 2 {
		t.Fatalf("unexpected report dimensions: %+v", report)
	}
	if report.Totals[0].Currency != "EUR" || report.Totals[1].GrossCents != 1500 || report.Totals[1].RefundedCents != 500 || report.Totals[1].NetCents != 1000 {
		t.Fatalf("incorrect currency totals: %+v", report.Totals)
	}
	for _, customer := range report.Customers {
		if customer.Email == "reader@example.com" && (customer.Orders != 2 || customer.NetCents != 1000 || customer.PublicationCount != 1) {
			t.Fatalf("incorrect customer aggregate: %+v", customer)
		}
	}
}
