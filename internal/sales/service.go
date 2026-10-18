package sales

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
)

type Service struct {
	store          Store
	provider       Provider
	stripeEnabled  bool
	webhookSecret  string
	secret         []byte
	baseURL        string
	successURL     string
	cancelURL      string
	cookieName     string
	cookieDomain   string
	cookieSecure   bool
	cookieSameSite http.SameSite
	purchaserTTL   time.Duration
	now            func() time.Time
	random         io.Reader
}

var _ Store = (*database.DB)(nil)

func New(store Store, options Options) (*Service, error) {
	var provider Provider
	if options.SecretKey != "" {
		stripe, err := NewStripeProvider(options.SecretKey, options.APIBaseURL, options.HTTPClient)
		if err != nil {
			return nil, err
		}
		provider = stripe
	}
	return NewService(store, provider, options)
}

func NewService(store Store, provider Provider, options Options) (*Service, error) {
	if store == nil {
		return nil, errors.New("sales store is required")
	}
	if options.StripeEnabled && (provider == nil || options.WebhookSecret == "") {
		return nil, ErrNotConfigured
	}
	if options.StripeEnabled && len(options.SigningSecret) < 32 {
		return nil, errors.New("purchaser signing secret must contain at least 32 bytes")
	}
	if options.SigningSecret != "" && len(options.SigningSecret) < 32 {
		return nil, errors.New("purchaser signing secret must contain at least 32 bytes")
	}
	if options.CookieName == "" {
		options.CookieName = DefaultCookieName
	}
	if options.CookieSameSite == 0 || options.CookieSameSite == http.SameSiteDefaultMode {
		options.CookieSameSite = http.SameSiteLaxMode
	}
	if options.PurchaserTTL == 0 {
		options.PurchaserTTL = 365 * 24 * time.Hour
	}
	if options.PurchaserTTL < 5*time.Minute || options.PurchaserTTL > 10*365*24*time.Hour {
		return nil, errors.New("purchaser cookie TTL must be between five minutes and ten years")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.BaseURL != "" {
		if err := validateAbsoluteURL(options.BaseURL, false); err != nil {
			return nil, invalid("invalid sales base URL")
		}
	}
	return &Service{
		store: store, provider: provider, stripeEnabled: options.StripeEnabled,
		webhookSecret: options.WebhookSecret, secret: []byte(options.SigningSecret),
		baseURL: strings.TrimRight(options.BaseURL, "/"), successURL: options.SuccessURL, cancelURL: options.CancelURL,
		cookieName: options.CookieName, cookieDomain: options.CookieDomain, cookieSecure: options.CookieSecure,
		cookieSameSite: options.CookieSameSite, purchaserTTL: options.PurchaserTTL,
		now: options.Now, random: options.Random,
	}, nil
}

func (s *Service) timeNow() time.Time { return s.now().UTC() }

func (s *Service) Configured() bool { return s.stripeEnabled && s.provider != nil }

func (s *Service) Publication(ctx context.Context, workspaceID, flipbookID string) (*models.Flipbook, error) {
	if workspaceID == "" {
		workspaceID = models.DefaultWorkspaceID
	}
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	return fb, normalizeError(err)
}

func (s *Service) Quote(ctx context.Context, workspaceID, flipbookID string) (*Quote, error) {
	fb, err := s.publicationForCheckout(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, err
	}
	return &Quote{Flipbook: fb, Price: FormatAmount(fb.Sales.PriceCents, fb.Sales.Currency), Configured: s.Configured()}, nil
}

func (s *Service) publicationForCheckout(ctx context.Context, workspaceID, flipbookID string) (*models.Flipbook, error) {
	fb, err := s.Publication(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, err
	}
	if !fb.IsPublished(s.timeNow()) || !fb.AllowsAnonymousAccess(s.timeNow()) {
		return nil, ErrNotFound
	}
	if !fb.Sales.Enabled {
		return nil, invalid("digital sales are not enabled for this publication")
	}
	if err := ValidateSettings(fb.Sales, fb.PageCount); err != nil {
		return nil, err
	}
	return fb, nil
}

func ValidateSettings(settings models.SalesSettings, pageCount int) error {
	currency, err := NormalizeCurrency(settings.Currency)
	if err != nil {
		return err
	}
	_ = currency
	if settings.PriceCents < 0 || settings.PriceCents > 1<<62 {
		return invalid("price must be a non-negative minor-unit amount")
	}
	if settings.Enabled && settings.PriceCents < 1 {
		return invalid("paid publications require a price greater than zero")
	}
	if settings.PreviewPages < 0 || (pageCount > 0 && settings.PreviewPages > pageCount) {
		return invalid("preview pages must be between zero and the publication page count")
	}
	if len(settings.StripePriceID) > 255 || strings.ContainsAny(settings.StripePriceID, "\r\n") {
		return invalid("Stripe price id is invalid")
	}
	return nil
}

func (s *Service) UpdateSettings(ctx context.Context, workspaceID, flipbookID string, settings models.SalesSettings) (*models.Flipbook, error) {
	fb, err := s.Publication(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, err
	}
	currency, err := NormalizeCurrency(settings.Currency)
	if err != nil {
		return nil, err
	}
	settings.Currency = currency
	settings.StripePriceID = strings.TrimSpace(settings.StripePriceID)
	if err := ValidateSettings(settings, fb.PageCount); err != nil {
		return nil, err
	}
	fb.Sales = settings
	if err := s.store.UpdateFlipbookMetadata(ctx, fb); err != nil {
		return nil, normalizeError(err)
	}
	return fb, nil
}

func NormalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if email == "" || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return "", invalid("a valid purchaser email is required")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) || !strings.Contains(email, "@") {
		return "", invalid("a valid purchaser email is required")
	}
	return email, nil
}

func (s *Service) Checkout(ctx context.Context, input CheckoutInput) (*CheckoutResult, error) {
	if !s.Configured() {
		return nil, ErrNotConfigured
	}
	fb, err := s.publicationForCheckout(ctx, input.WorkspaceID, input.FlipbookID)
	if err != nil {
		return nil, err
	}
	email, err := NormalizeEmail(input.Email)
	if err != nil {
		return nil, err
	}
	purchaseID, err := randomID(s.random)
	if err != nil {
		return nil, err
	}
	now := s.timeNow()
	purchase := &models.Purchase{
		ID: purchaseID, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Email: email, EmailHash: purchaserIdentity(purchaseID, email),
		AmountCents: fb.Sales.PriceCents, Currency: fb.Sales.Currency,
		Status: models.PurchasePending, CreatedAt: now, UpdatedAt: now,
		Metadata: map[string]string{"source": "flipbook_checkout"},
	}
	successURL, err := s.flowURL(s.successURL, "/sales/success", "session_id", "{CHECKOUT_SESSION_ID}", purchaseID, fb.ID)
	if err != nil {
		return nil, err
	}
	cancelURL, err := s.flowURL(s.cancelURL, "/sales/cancel", "purchase_id", purchaseID, purchaseID, fb.ID)
	if err != nil {
		return nil, err
	}
	session, err := s.provider.CreateCheckoutSession(ctx, CheckoutSessionInput{
		PurchaseID: purchaseID, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Title: fb.Title, Email: email, AmountCents: purchase.AmountCents, Currency: purchase.Currency,
		PriceID: fb.Sales.StripePriceID, SuccessURL: successURL, CancelURL: cancelURL,
		Idempotency: "checkout-" + purchaseID,
	})
	if err != nil {
		return nil, err
	}
	if session == nil || session.ID == "" || !validHostedCheckoutURL(session.URL) {
		if session != nil && session.ID != "" {
			s.expireAfterFailure(session.ID)
		}
		return nil, fmt.Errorf("%w: invalid checkout session response", ErrProvider)
	}
	purchase.StripeCheckoutSessionID = session.ID
	if err := s.store.CreatePurchase(ctx, purchase); err != nil {
		s.expireAfterFailure(session.ID)
		return nil, normalizeError(err)
	}
	return &CheckoutResult{CheckoutURL: session.URL, Purchase: purchase}, nil
}

func (s *Service) expireAfterFailure(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.provider.ExpireCheckoutSession(ctx, sessionID)
}

func (s *Service) flowURL(configured, defaultPath, queryName, queryValue, purchaseID, flipbookID string) (string, error) {
	raw := configured
	if raw == "" {
		raw = s.baseURL + defaultPath
	}
	raw = strings.ReplaceAll(raw, "{PURCHASE_ID}", purchaseID)
	raw = strings.ReplaceAll(raw, "{FLIPBOOK_ID}", flipbookID)
	const sessionPlaceholder = "FLIPBOOKCHECKOUTSESSIONID"
	raw = strings.ReplaceAll(raw, "{CHECKOUT_SESSION_ID}", sessionPlaceholder)
	if queryName == "session_id" {
		queryValue = sessionPlaceholder
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", invalid("invalid checkout return URL")
	}
	query := parsed.Query()
	query.Set(queryName, queryValue)
	parsed.RawQuery = query.Encode()
	if err := validateAbsoluteURL(parsed.String(), true); err != nil {
		return "", invalid("invalid checkout return URL")
	}
	return strings.ReplaceAll(parsed.String(), sessionPlaceholder, "{CHECKOUT_SESSION_ID}"), nil
}

func validateAbsoluteURL(raw string, allowQuery bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !loopbackHTTP(parsed)) || (!allowQuery && parsed.RawQuery != "") {
		return invalid("invalid absolute URL; HTTPS is required except on loopback")
	}
	return nil
}

func (s *Service) CompleteCheckout(ctx context.Context, sessionID string) (*models.Purchase, error) {
	if !s.Configured() {
		return nil, ErrNotConfigured
	}
	if sessionID == "" || len(sessionID) > 255 {
		return nil, invalid("checkout session id is required")
	}
	session, err := s.provider.RetrieveCheckoutSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	purchase, err := s.completeSession(ctx, "", session)
	if err != nil {
		return nil, err
	}
	if purchase.Status != models.PurchasePaid {
		return nil, ErrPaymentIncomplete
	}
	return purchase, nil
}

func (s *Service) completeSession(ctx context.Context, eventID string, session *CheckoutSession) (*models.Purchase, error) {
	if session == nil || session.ID == "" {
		return nil, invalid("checkout session is missing")
	}
	purchase, err := s.store.GetPurchaseByCheckoutSession(ctx, session.ID)
	if err != nil {
		return nil, normalizeError(err)
	}
	if err := validateSessionForPurchase(session, purchase); err != nil {
		return nil, err
	}
	if purchase.Status == models.PurchaseRefunded || purchase.Status == models.PurchaseCancelled || purchase.Status == models.PurchaseFailed {
		return purchase, nil
	}
	if session.PaymentStatus != "paid" {
		return nil, ErrPaymentIncomplete
	}
	entitlement := &models.Entitlement{EmailHash: purchase.EmailHash}
	if err := s.store.MarkPurchasePaid(ctx, purchase.ID, eventID, entitlement); err != nil {
		return nil, normalizeError(err)
	}
	purchase, err = s.store.GetPurchase(ctx, purchase.WorkspaceID, purchase.ID)
	if err != nil {
		return nil, normalizeError(err)
	}
	return purchase, nil
}

func validateSessionForPurchase(session *CheckoutSession, purchase *models.Purchase) error {
	if session.ClientReferenceID != purchase.ID || session.Metadata["purchase_id"] != purchase.ID ||
		session.Metadata["workspace_id"] != purchase.WorkspaceID || session.Metadata["flipbook_id"] != purchase.FlipbookID ||
		session.AmountTotal != purchase.AmountCents || !strings.EqualFold(session.Currency, purchase.Currency) {
		return fmt.Errorf("%w: checkout session does not match the purchase", ErrConflict)
	}
	return nil
}

func (s *Service) CookieForPurchase(ctx context.Context, workspaceID, purchaseID string) (*http.Cookie, error) {
	if err := validatePurchaseID(purchaseID); err != nil {
		return nil, err
	}
	purchase, err := s.store.GetPurchase(ctx, workspaceID, purchaseID)
	if err != nil {
		return nil, normalizeError(err)
	}
	if purchase.Status != models.PurchasePaid {
		return nil, ErrPaymentIncomplete
	}
	active, err := s.store.HasEntitlement(ctx, purchase.WorkspaceID, purchase.FlipbookID, "", purchase.EmailHash, s.timeNow())
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrConflict
	}
	return s.issuePurchaserCookie(purchase)
}

type CancelResult struct {
	Purchase *models.Purchase
	Flipbook *models.Flipbook
	Cookie   *http.Cookie
}

func (s *Service) CancelCheckout(ctx context.Context, workspaceID, purchaseID string) (*CancelResult, error) {
	purchase, err := s.store.GetPurchase(ctx, workspaceID, purchaseID)
	if err != nil {
		return nil, normalizeError(err)
	}
	fb, err := s.Publication(ctx, purchase.WorkspaceID, purchase.FlipbookID)
	if err != nil {
		return nil, err
	}
	result := &CancelResult{Purchase: purchase, Flipbook: fb}
	if purchase.Status == models.PurchasePaid {
		cookie, err := s.CookieForPurchase(ctx, purchase.WorkspaceID, purchase.ID)
		if err != nil {
			return nil, err
		}
		result.Cookie = cookie
	}
	return result, nil
}

type webhookEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signature string) error {
	if !s.stripeEnabled || s.webhookSecret == "" {
		return ErrNotConfigured
	}
	if len(payload) == 0 || len(payload) > maxProviderResponseBytes {
		return invalid("webhook payload is empty or too large")
	}
	if err := VerifyWebhookSignature(payload, signature, s.webhookSecret, s.timeNow(), DefaultWebhookTolerance); err != nil {
		return err
	}
	var event webhookEvent
	if err := json.Unmarshal(payload, &event); err != nil || event.ID == "" || event.Type == "" || len(event.Data.Object) == 0 {
		return invalid("webhook event is invalid")
	}
	switch event.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var session CheckoutSession
		if err := json.Unmarshal(event.Data.Object, &session); err != nil {
			return invalid("checkout session webhook is invalid")
		}
		_, err := s.completeSession(ctx, event.ID, &session)
		if errors.Is(err, ErrPaymentIncomplete) {
			return nil
		}
		return err
	case "charge.refunded":
		return s.handleRefundWebhook(ctx, event.ID, event.Data.Object)
	default:
		return nil
	}
}

type chargeRefund struct {
	ID             string            `json:"id"`
	Amount         int64             `json:"amount"`
	AmountRefunded int64             `json:"amount_refunded"`
	Currency       string            `json:"currency"`
	Refunded       bool              `json:"refunded"`
	Metadata       map[string]string `json:"metadata"`
}

func (s *Service) handleRefundWebhook(ctx context.Context, eventID string, data []byte) error {
	var charge chargeRefund
	if err := json.Unmarshal(data, &charge); err != nil {
		return invalid("refund webhook is invalid")
	}
	purchaseID := charge.Metadata["purchase_id"]
	if purchaseID == "" {
		return nil
	}
	workspaceID := charge.Metadata["workspace_id"]
	purchase, err := s.store.GetPurchase(ctx, workspaceID, purchaseID)
	if err != nil {
		return normalizeError(err)
	}
	if purchase.FlipbookID != charge.Metadata["flipbook_id"] || charge.Amount != purchase.AmountCents ||
		!strings.EqualFold(charge.Currency, purchase.Currency) {
		return fmt.Errorf("%w: refund does not match the purchase", ErrConflict)
	}
	if charge.AmountRefunded < charge.Amount || charge.AmountRefunded == 0 {
		return nil
	}
	return normalizeError(s.store.RefundPurchase(ctx, purchase.WorkspaceID, purchase.ID, eventID, "refunded by payment provider"))
}

func (s *Service) Refund(ctx context.Context, workspaceID, purchaseID, reason string) error {
	purchase, err := s.store.GetPurchase(ctx, workspaceID, purchaseID)
	if err != nil {
		return normalizeError(err)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "refund requested by administrator"
	}
	if len(reason) > 500 {
		return invalid("refund reason is too long")
	}
	if purchase.Status == models.PurchaseRefunded {
		return normalizeError(s.store.RefundPurchase(ctx, workspaceID, purchaseID, "", reason))
	}
	if purchase.Status != models.PurchasePaid {
		return fmt.Errorf("%w: only paid purchases can be refunded", ErrConflict)
	}
	if !s.Configured() {
		return ErrNotConfigured
	}
	session, err := s.provider.RetrieveCheckoutSession(ctx, purchase.StripeCheckoutSessionID)
	if err != nil {
		return err
	}
	if err := validateSessionForPurchase(session, purchase); err != nil {
		return err
	}
	if session.PaymentIntentID == "" {
		return fmt.Errorf("%w: checkout session has no payment intent", ErrProvider)
	}
	refund, err := s.provider.CreateRefund(ctx, RefundInput{
		PaymentIntentID: session.PaymentIntentID, PurchaseID: purchase.ID,
		WorkspaceID: purchase.WorkspaceID, FlipbookID: purchase.FlipbookID,
		Reason: reason, Idempotency: "refund-" + purchase.ID,
	})
	if err != nil {
		return err
	}
	if refund != nil && refund.Status == "pending" {
		return ErrRefundPending
	}
	if refund == nil || (refund.Status != "" && refund.Status != "succeeded") {
		return fmt.Errorf("%w: refund was not accepted", ErrProvider)
	}
	return normalizeError(s.store.RefundPurchase(ctx, workspaceID, purchaseID, "", reason))
}

func (s *Service) Revoke(ctx context.Context, workspaceID, purchaseID, reason string) error {
	purchase, err := s.store.GetPurchase(ctx, workspaceID, purchaseID)
	if err != nil {
		return normalizeError(err)
	}
	if purchase.Status != models.PurchasePaid {
		return fmt.Errorf("%w: only paid purchase access can be revoked", ErrConflict)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "access revoked by administrator"
	}
	if len(reason) > 500 {
		return invalid("revocation reason is too long")
	}
	entitlement, err := s.store.GetEntitlementByPurchase(ctx, purchase.ID)
	if err != nil {
		return normalizeError(err)
	}
	return normalizeError(s.store.RevokeEntitlement(ctx, workspaceID, entitlement.ID, reason))
}
