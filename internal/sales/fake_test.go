package sales

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type fakeSalesStore struct {
	flipbooks    map[string]*models.Flipbook
	purchases    map[string]*models.Purchase
	entitlements map[string]*models.Entitlement
	createErr    error
}

func newFakeSalesStore() *fakeSalesStore {
	return &fakeSalesStore{
		flipbooks: make(map[string]*models.Flipbook), purchases: make(map[string]*models.Purchase),
		entitlements: make(map[string]*models.Entitlement),
	}
}

func salesKey(workspaceID, id string) string { return workspaceID + "/" + id }

func copyPurchase(purchase *models.Purchase) *models.Purchase {
	if purchase == nil {
		return nil
	}
	clone := *purchase
	clone.Metadata = make(map[string]string, len(purchase.Metadata))
	for key, value := range purchase.Metadata {
		clone.Metadata[key] = value
	}
	return &clone
}

func (f *fakeSalesStore) GetFlipbookInWorkspace(_ context.Context, workspaceID, id string) (*models.Flipbook, error) {
	fb := f.flipbooks[salesKey(workspaceID, id)]
	if fb == nil {
		return nil, mongo.ErrNoDocuments
	}
	clone := *fb
	return &clone, nil
}

func (f *fakeSalesStore) UpdateFlipbookMetadata(_ context.Context, fb *models.Flipbook) error {
	if f.flipbooks[salesKey(fb.WorkspaceID, fb.ID)] == nil {
		return mongo.ErrNoDocuments
	}
	clone := *fb
	f.flipbooks[salesKey(fb.WorkspaceID, fb.ID)] = &clone
	return nil
}

func (f *fakeSalesStore) CreatePurchase(_ context.Context, purchase *models.Purchase) error {
	if f.createErr != nil {
		return f.createErr
	}
	for _, existing := range f.purchases {
		if existing.StripeCheckoutSessionID != "" && existing.StripeCheckoutSessionID == purchase.StripeCheckoutSessionID {
			return database.ErrConflict
		}
	}
	f.purchases[purchase.ID] = copyPurchase(purchase)
	return nil
}

func (f *fakeSalesStore) GetPurchase(_ context.Context, workspaceID, purchaseID string) (*models.Purchase, error) {
	purchase := f.purchases[purchaseID]
	if purchase == nil || (workspaceID != "" && purchase.WorkspaceID != workspaceID) {
		return nil, mongo.ErrNoDocuments
	}
	return copyPurchase(purchase), nil
}

func (f *fakeSalesStore) GetPurchaseByCheckoutSession(_ context.Context, sessionID string) (*models.Purchase, error) {
	for _, purchase := range f.purchases {
		if purchase.StripeCheckoutSessionID == sessionID {
			return copyPurchase(purchase), nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeSalesStore) MarkPurchasePaid(_ context.Context, purchaseID, eventID string, entitlement *models.Entitlement) error {
	purchase := f.purchases[purchaseID]
	if purchase == nil {
		return mongo.ErrNoDocuments
	}
	now := time.Now().UTC()
	if purchase.Status != models.PurchasePaid {
		if purchase.Status != models.PurchasePending {
			return database.ErrConflict
		}
		purchase.Status = models.PurchasePaid
		purchase.PurchasedAt = &now
		purchase.StripeEventID = eventID
	}
	if entitlement == nil {
		return errors.New("entitlement required")
	}
	for _, existing := range f.entitlements {
		if existing.PurchaseID != purchaseID && existing.WorkspaceID == purchase.WorkspaceID &&
			existing.FlipbookID == purchase.FlipbookID && existing.EmailHash == purchase.EmailHash {
			return database.ErrConflict
		}
	}
	if f.entitlements[purchaseID] != nil {
		return nil
	}
	clone := *entitlement
	clone.ID = "entitlement-" + purchaseID
	clone.WorkspaceID, clone.FlipbookID, clone.PurchaseID = purchase.WorkspaceID, purchase.FlipbookID, purchaseID
	clone.EmailHash = purchase.EmailHash
	clone.Status = models.EntitlementActive
	clone.GrantedAt = now
	f.entitlements[purchaseID] = &clone
	return nil
}

func (f *fakeSalesStore) RefundPurchase(_ context.Context, workspaceID, purchaseID, eventID, reason string) error {
	purchase := f.purchases[purchaseID]
	if purchase == nil || purchase.WorkspaceID != workspaceID ||
		(purchase.Status != models.PurchasePaid && purchase.Status != models.PurchaseRefunded) {
		return database.ErrConflict
	}
	now := time.Now().UTC()
	purchase.Status = models.PurchaseRefunded
	purchase.RefundedAt = &now
	if eventID != "" {
		purchase.StripeEventID = eventID
	}
	if entitlement := f.entitlements[purchaseID]; entitlement != nil {
		entitlement.Status = models.EntitlementRevoked
		entitlement.RevokedAt = &now
		entitlement.RevocationReason = reason
	}
	return nil
}

func (f *fakeSalesStore) ListPurchases(_ context.Context, filter database.PurchaseFilter) ([]*models.Purchase, error) {
	var result []*models.Purchase
	for _, purchase := range f.purchases {
		if purchase.WorkspaceID != filter.WorkspaceID || (filter.FlipbookID != "" && purchase.FlipbookID != filter.FlipbookID) ||
			(filter.Status != "" && purchase.Status != filter.Status) || (!filter.From.IsZero() && purchase.CreatedAt.Before(filter.From)) ||
			(!filter.To.IsZero() && !purchase.CreatedAt.Before(filter.To)) {
			continue
		}
		result = append(result, copyPurchase(purchase))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if filter.Offset >= int64(len(result)) {
		return []*models.Purchase{}, nil
	}
	result = result[filter.Offset:]
	if filter.Limit > 0 && int64(len(result)) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (f *fakeSalesStore) HasEntitlement(_ context.Context, workspaceID, flipbookID, userID, emailHash string, now time.Time) (bool, error) {
	for _, entitlement := range f.entitlements {
		if entitlement.WorkspaceID == workspaceID && entitlement.FlipbookID == flipbookID &&
			((userID != "" && entitlement.UserID == userID) || (emailHash != "" && entitlement.EmailHash == emailHash)) {
			return entitlement.Active(now), nil
		}
	}
	return false, nil
}

func (f *fakeSalesStore) GetEntitlementByPurchase(_ context.Context, purchaseID string) (*models.Entitlement, error) {
	entitlement := f.entitlements[purchaseID]
	if entitlement == nil {
		return nil, mongo.ErrNoDocuments
	}
	clone := *entitlement
	return &clone, nil
}

func (f *fakeSalesStore) RevokeEntitlement(_ context.Context, workspaceID, entitlementID, reason string) error {
	for _, entitlement := range f.entitlements {
		if entitlement.ID == entitlementID && entitlement.WorkspaceID == workspaceID {
			now := time.Now().UTC()
			entitlement.Status = models.EntitlementRevoked
			entitlement.RevokedAt = &now
			entitlement.RevocationReason = reason
			return nil
		}
	}
	return mongo.ErrNoDocuments
}

type fakeSalesProvider struct {
	sessions     map[string]*CheckoutSession
	lastCreate   CheckoutSessionInput
	lastRefund   RefundInput
	refundStatus string
	createErr    error
	retrieveErr  error
	refundErr    error
	expired      []string
	refundCalls  int
}

func newFakeSalesProvider() *fakeSalesProvider {
	return &fakeSalesProvider{sessions: make(map[string]*CheckoutSession), refundStatus: "succeeded"}
}

func (f *fakeSalesProvider) CreateCheckoutSession(_ context.Context, input CheckoutSessionInput) (*CheckoutSession, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.lastCreate = input
	session := &CheckoutSession{ID: "cs_" + input.PurchaseID, URL: "https://checkout.example.test/pay/cs_" + input.PurchaseID}
	f.sessions[session.ID] = session
	return session, nil
}

func (f *fakeSalesProvider) RetrieveCheckoutSession(_ context.Context, sessionID string) (*CheckoutSession, error) {
	if f.retrieveErr != nil {
		return nil, f.retrieveErr
	}
	session := f.sessions[sessionID]
	if session == nil {
		return nil, ErrProvider
	}
	clone := *session
	return &clone, nil
}

func (f *fakeSalesProvider) ExpireCheckoutSession(_ context.Context, sessionID string) error {
	f.expired = append(f.expired, sessionID)
	return nil
}

func (f *fakeSalesProvider) CreateRefund(_ context.Context, input RefundInput) (*Refund, error) {
	f.refundCalls++
	if f.refundErr != nil {
		return nil, f.refundErr
	}
	f.lastRefund = input
	return &Refund{ID: "re_test", Status: f.refundStatus}, nil
}

func testSalesPublication(now time.Time) *models.Flipbook {
	fb := &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", OwnerID: "owner", Title: "Annual report", Slug: "annual-report",
		Status: models.StatusReady, PublicationState: models.PublicationPublished, Visibility: models.VisibilityPublic,
		PageCount: 5, Sales: models.SalesSettings{Enabled: true, PriceCents: 1250, Currency: "USD", PreviewPages: 2},
		CreatedAt: now.Add(-time.Hour),
	}
	fb.ApplyDefaults()
	return fb
}

func newSalesFixture(t *testing.T) (*Service, *fakeSalesStore, *fakeSalesProvider, *models.Flipbook, time.Time) {
	t.Helper()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	store := newFakeSalesStore()
	provider := newFakeSalesProvider()
	fb := testSalesPublication(now)
	store.flipbooks[salesKey(fb.WorkspaceID, fb.ID)] = fb
	service, err := NewService(store, provider, Options{
		StripeEnabled: true, WebhookSecret: "whsec_test", SigningSecret: strings.Repeat("signing-secret", 3),
		BaseURL: "https://flipbook.example.test", CookieSecure: true, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, provider, fb, now
}

func sessionForPurchase(purchase *models.Purchase) *CheckoutSession {
	return &CheckoutSession{
		ID: purchase.StripeCheckoutSessionID, ClientReferenceID: purchase.ID, PaymentIntentID: "pi_" + purchase.ID,
		PaymentStatus: "paid", Status: "complete", AmountTotal: purchase.AmountCents, Currency: purchase.Currency,
		CustomerEmail: purchase.Email, Metadata: map[string]string{
			"purchase_id": purchase.ID, "workspace_id": purchase.WorkspaceID, "flipbook_id": purchase.FlipbookID,
		},
	}
}

func startTestCheckout(t *testing.T, service *Service, provider *fakeSalesProvider, email string) *models.Purchase {
	t.Helper()
	result, err := service.Checkout(context.Background(), CheckoutInput{WorkspaceID: "workspace", FlipbookID: "book", Email: email})
	if err != nil {
		t.Fatal(err)
	}
	provider.sessions[result.Purchase.StripeCheckoutSessionID] = sessionForPurchase(result.Purchase)
	return result.Purchase
}

func completeTestCheckout(t *testing.T, service *Service, purchase *models.Purchase) *models.Purchase {
	t.Helper()
	completed, err := service.CompleteCheckout(context.Background(), purchase.StripeCheckoutSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}
