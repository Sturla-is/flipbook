package engagement

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
)

var (
	ErrInvalidInput     = errors.New("invalid engagement input")
	ErrConsentRequired  = errors.New("explicit consent is required")
	ErrNotConfigured    = errors.New("engagement capability is not configured")
	ErrNotFound         = errors.New("engagement resource not found")
	ErrForbidden        = errors.New("engagement operation is not permitted")
	ErrInvalidToken     = errors.New("subscription token is invalid or expired")
	ErrDelivery         = errors.New("external delivery failed")
	ErrRateLimit        = errors.New("too many engagement requests")
	ErrConfirmationSent = errors.New("subscription confirmation is required")
)

type LeadGrantService interface {
	IssueLeadGrant(ctx context.Context, workspaceID, flipbookID string, captured, dismissed bool, request publishing.AccessRequest) (*publishing.GrantResult, error)
	AuthorizeViewer(ctx context.Context, workspaceID, flipbookID string, request publishing.AccessRequest) (*publishing.Authorization, error)
	GrantFromRequest(request *http.Request, flipbookID string) string
}

type Store interface {
	GetFlipbookContext(ctx context.Context, id string) (*models.Flipbook, error)
	GetFlipbookInWorkspace(ctx context.Context, workspaceID, id string) (*models.Flipbook, error)
	UpdateFlipbookMetadata(ctx context.Context, fb *models.Flipbook) error
	SearchFlipbooks(ctx context.Context, filter database.FlipbookFilter) ([]*models.Flipbook, int64, error)
	GetWorkspace(ctx context.Context, workspaceID string) (*models.Workspace, error)

	UpsertLead(ctx context.Context, lead *models.Lead) error
	ListLeads(ctx context.Context, filter database.LeadFilter) ([]*models.Lead, error)
	CountLeads(ctx context.Context, workspaceID, flipbookID string) (int64, error)
	DeleteLead(ctx context.Context, workspaceID, leadID string) error
	DeleteLeadsForFlipbook(ctx context.Context, workspaceID, flipbookID string) error

	UpsertSubscriber(ctx context.Context, subscriber *models.Subscriber) error
	GetSubscriber(ctx context.Context, workspaceID, email string) (*models.Subscriber, error)
	ConfirmSubscriber(ctx context.Context, token string) error
	UnsubscribeSubscriber(ctx context.Context, token string) error
	ListSubscribers(ctx context.Context, workspaceID string, status models.SubscriberStatus) ([]*models.Subscriber, error)
	DeleteSubscriber(ctx context.Context, workspaceID, subscriberID string) error

	CreateWebhookSubscription(ctx context.Context, subscription *models.WebhookSubscription) error
	ListWebhookSubscriptions(ctx context.Context, workspaceID, eventType string, enabledOnly bool) ([]*models.WebhookSubscription, error)
	SetWebhookSubscriptionEnabled(ctx context.Context, workspaceID, subscriptionID string, enabled bool) error
	EnqueueWebhookDelivery(ctx context.Context, delivery *models.WebhookDelivery) error
	ListWebhookDeliveries(ctx context.Context, workspaceID, subscriptionID string, limit int64) ([]*models.WebhookDelivery, error)

	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

var _ Store = (*database.DB)(nil)

type Options struct {
	SigningSecret     string
	EncryptionKey     string
	BaseURL           string
	LeadRetentionDays int
	ConfirmationTTL   time.Duration
	Publisher         LeadGrantService
	Mailer            Mailer
	CRM               *CRMDispatcher
	Webhooks          config.WebhookConfig
	Now               func() time.Time
	Random            io.Reader
}

type Service struct {
	store             Store
	publisher         LeadGrantService
	mailer            Mailer
	crm               *CRMDispatcher
	webhooks          config.WebhookConfig
	signingSecret     []byte
	encryptionKey     []byte
	baseURL           string
	leadRetentionDays int
	confirmationTTL   time.Duration
	now               func() time.Time
	random            io.Reader
}

func New(store Store, options Options) (*Service, error) {
	if store == nil {
		return nil, errors.New("engagement store is required")
	}
	if len(options.SigningSecret) < 32 {
		return nil, errors.New("engagement signing secret must contain at least 32 bytes")
	}
	if options.EncryptionKey == "" {
		options.EncryptionKey = options.SigningSecret
	}
	if len(options.EncryptionKey) < 32 {
		return nil, errors.New("engagement encryption key must contain at least 32 bytes")
	}
	if options.BaseURL == "" {
		options.BaseURL = "http://localhost:8080"
	}
	baseURL, err := normalizeBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if options.LeadRetentionDays == 0 {
		options.LeadRetentionDays = 365
	}
	if options.LeadRetentionDays < 0 || options.LeadRetentionDays > 3650 {
		return nil, errors.New("lead retention must be between zero and 3650 days")
	}
	if options.ConfirmationTTL == 0 {
		options.ConfirmationTTL = 48 * time.Hour
	}
	if options.ConfirmationTTL < time.Hour || options.ConfirmationTTL > 7*24*time.Hour {
		return nil, errors.New("subscriber confirmation TTL must be between one hour and seven days")
	}
	if options.Webhooks.TimeoutSeconds == 0 {
		options.Webhooks.TimeoutSeconds = 15
	}
	if options.Webhooks.MaxAttempts == 0 {
		options.Webhooks.MaxAttempts = 8
	}
	if options.Webhooks.RetryBackoffSeconds == 0 {
		options.Webhooks.RetryBackoffSeconds = 30
	}
	if options.Webhooks.MaxResponseBodyBytes == 0 {
		options.Webhooks.MaxResponseBodyBytes = 65536
	}
	if options.Webhooks.TimeoutSeconds < 1 || options.Webhooks.MaxAttempts < 1 || options.Webhooks.RetryBackoffSeconds < 1 || options.Webhooks.MaxResponseBodyBytes < 0 {
		return nil, errors.New("webhook timeout, attempts, backoff, and response limits are invalid")
	}
	if options.Webhooks.SigningSecret != "" && len(options.Webhooks.SigningSecret) < 32 {
		return nil, errors.New("webhook signing secret must contain at least 32 bytes")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.CRM == nil {
		options.CRM = NewCRMDispatcher(config.CRMConfig{}, nil)
	}
	return &Service{
		store: store, publisher: options.Publisher, mailer: options.Mailer, crm: options.CRM,
		webhooks: options.Webhooks, signingSecret: []byte(options.SigningSecret),
		encryptionKey: []byte(options.EncryptionKey), baseURL: baseURL,
		leadRetentionDays: options.LeadRetentionDays, confirmationTTL: options.ConfirmationTTL,
		now: options.Now, random: options.Random,
	}, nil
}

func NewWithConfig(store Store, cfg *config.Config, publisher LeadGrantService) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("engagement configuration is required")
	}
	encryptionKey := cfg.Security.TokenEncryptionKey
	if encryptionKey == "" {
		encryptionKey = cfg.SessionSecret
	}
	return New(store, Options{
		SigningSecret: cfg.SessionSecret, EncryptionKey: encryptionKey, BaseURL: cfg.BaseURL,
		Publisher: publisher, Mailer: NewSMTPMailer(cfg.SMTP),
		CRM: NewCRMDispatcher(cfg.CRM, nil), Webhooks: cfg.Webhooks,
	})
}

func normalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("engagement base URL must be an absolute HTTP or HTTPS URL without query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (s *Service) timeNow() time.Time { return s.now().UTC() }

func (s *Service) GrantFromRequest(r *http.Request, flipbookID string) string {
	if s.publisher == nil {
		return ""
	}
	return s.publisher.GrantFromRequest(r, flipbookID)
}

type DeliveryResult struct {
	Provider  string `json:"provider"`
	Delivered bool   `json:"delivered"`
	Error     string `json:"error,omitempty"`
}
