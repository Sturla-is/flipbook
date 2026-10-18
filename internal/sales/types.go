package sales

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
)

const (
	DefaultCookieName = "flipbook_purchaser"
	defaultAPIBaseURL = "https://api.stripe.com"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	StripeEnabled  bool
	SecretKey      string
	WebhookSecret  string
	SigningSecret  string
	BaseURL        string
	SuccessURL     string
	CancelURL      string
	APIBaseURL     string
	HTTPClient     HTTPClient
	CookieName     string
	CookieDomain   string
	CookieSecure   bool
	CookieSameSite http.SameSite
	PurchaserTTL   time.Duration
	Now            func() time.Time
	Random         io.Reader
}

type CheckoutInput struct {
	WorkspaceID string
	FlipbookID  string
	Email       string
}

type CheckoutResult struct {
	CheckoutURL string
	Purchase    *models.Purchase
}

type CheckoutSessionInput struct {
	PurchaseID  string
	WorkspaceID string
	FlipbookID  string
	Title       string
	Email       string
	AmountCents int64
	Currency    string
	PriceID     string
	SuccessURL  string
	CancelURL   string
	Idempotency string
}

type CheckoutSession struct {
	ID                string `json:"id"`
	URL               string `json:"url"`
	ClientReferenceID string `json:"client_reference_id"`
	PaymentIntentID   string `json:"payment_intent"`
	PaymentStatus     string `json:"payment_status"`
	Status            string `json:"status"`
	AmountTotal       int64  `json:"amount_total"`
	Currency          string `json:"currency"`
	CustomerEmail     string `json:"customer_email"`
	CustomerDetails   struct {
		Email string `json:"email"`
	} `json:"customer_details"`
	Metadata map[string]string `json:"metadata"`
}

type RefundInput struct {
	PaymentIntentID string
	PurchaseID      string
	WorkspaceID     string
	FlipbookID      string
	Reason          string
	Idempotency     string
}

type Refund struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type Provider interface {
	CreateCheckoutSession(context.Context, CheckoutSessionInput) (*CheckoutSession, error)
	RetrieveCheckoutSession(context.Context, string) (*CheckoutSession, error)
	ExpireCheckoutSession(context.Context, string) error
	CreateRefund(context.Context, RefundInput) (*Refund, error)
}

type Store interface {
	GetFlipbookInWorkspace(context.Context, string, string) (*models.Flipbook, error)
	UpdateFlipbookMetadata(context.Context, *models.Flipbook) error
	CreatePurchase(context.Context, *models.Purchase) error
	GetPurchase(context.Context, string, string) (*models.Purchase, error)
	GetPurchaseByCheckoutSession(context.Context, string) (*models.Purchase, error)
	MarkPurchasePaid(context.Context, string, string, *models.Entitlement) error
	RefundPurchase(context.Context, string, string, string, string) error
	ListPurchases(context.Context, database.PurchaseFilter) ([]*models.Purchase, error)
	HasEntitlement(context.Context, string, string, string, string, time.Time) (bool, error)
	GetEntitlementByPurchase(context.Context, string) (*models.Entitlement, error)
	RevokeEntitlement(context.Context, string, string, string) error
}

type Quote struct {
	Flipbook   *models.Flipbook
	Price      string
	Configured bool
}

type ReportFilter struct {
	WorkspaceID string
	FlipbookID  string
	Status      models.PurchaseStatus
	From        time.Time
	To          time.Time
	Limit       int64
	Offset      int64
}

type Order struct {
	Purchase         *models.Purchase `json:"purchase"`
	PublicationTitle string           `json:"publication_title"`
}

type CurrencyTotal struct {
	Currency      string `json:"currency"`
	Orders        int64  `json:"orders"`
	GrossCents    int64  `json:"gross_cents"`
	RefundedCents int64  `json:"refunded_cents"`
	NetCents      int64  `json:"net_cents"`
}

type Customer struct {
	Email            string    `json:"email"`
	Currency         string    `json:"currency"`
	Orders           int64     `json:"orders"`
	GrossCents       int64     `json:"gross_cents"`
	RefundedCents    int64     `json:"refunded_cents"`
	NetCents         int64     `json:"net_cents"`
	PublicationCount int       `json:"publication_count"`
	FirstPurchasedAt time.Time `json:"first_purchased_at"`
	LastPurchasedAt  time.Time `json:"last_purchased_at"`
}

type Report struct {
	Orders     []*Order        `json:"orders"`
	Totals     []CurrencyTotal `json:"totals"`
	Customers  []Customer      `json:"customers"`
	Truncated  bool            `json:"truncated"`
	TotalCount int             `json:"total_count"`
}
