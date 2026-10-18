package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jonradoff/flipbook/internal/models"
)

func validWebhookEvent(event string, wildcard bool) bool {
	if event == "*" {
		return wildcard
	}
	if event == "" || len(event) > 64 {
		return false
	}
	for _, r := range event {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func normalizeWebhookEvents(events []string) ([]string, error) {
	if len(events) == 0 || len(events) > 32 {
		return nil, integrationError(ErrInvalidInput, "", "webhooks", "Provide between 1 and 32 webhook event types.")
	}
	seen := make(map[string]struct{}, len(events))
	normalized := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.ToLower(strings.TrimSpace(event))
		if !validWebhookEvent(event, true) {
			return nil, integrationError(ErrInvalidInput, "", "webhooks", "Webhook event types may contain lowercase letters, numbers, dots, underscores, and hyphens, or * for all events.")
		}
		if _, ok := seen[event]; ok {
			continue
		}
		seen[event] = struct{}{}
		normalized = append(normalized, event)
	}
	return normalized, nil
}

func publicWebhookSubscription(subscription *models.WebhookSubscription) *models.WebhookSubscription {
	if subscription == nil {
		return nil
	}
	clone := *subscription
	clone.SecretCiphertext = ""
	return &clone
}

func (s *Service) CreateWebhook(ctx context.Context, workspaceID, rawURL string, events []string) (*WebhookSubscriptionResult, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, integrationError(ErrInvalidInput, "", "webhooks", "A workspace is required for a webhook subscription.")
	}
	if s.cipher == nil {
		return nil, integrationError(ErrNotConfigured, "", "webhooks", "Set security.token_encryption_key before creating signed webhook subscriptions.")
	}
	rawURL = strings.TrimSpace(rawURL)
	if !secureProviderURL(rawURL) {
		return nil, integrationError(ErrInvalidInput, "", "webhooks", "Webhook URLs must be absolute HTTPS URLs without user information; HTTP is allowed only on loopback for development.")
	}
	normalized, err := normalizeWebhookEvents(events)
	if err != nil {
		return nil, err
	}
	secret, err := randomIntegrationToken(s.random)
	if err != nil {
		return nil, err
	}
	subscription := &models.WebhookSubscription{
		ID: uuid.NewString(), WorkspaceID: workspaceID, URL: rawURL, Events: normalized, Enabled: true,
	}
	subscription.SecretCiphertext, err = s.cipher.Seal(webhookSecretContext(subscription.ID), secret)
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateWebhookSubscription(ctx, subscription); err != nil {
		return nil, err
	}
	return &WebhookSubscriptionResult{Subscription: publicWebhookSubscription(subscription), Secret: secret}, nil
}

func (s *Service) WebhookSecret(subscription *models.WebhookSubscription) (string, error) {
	if subscription == nil || subscription.ID == "" {
		return "", integrationError(ErrNotFound, "", "webhooks", "The webhook subscription was not found.")
	}
	return s.cipher.Open(webhookSecretContext(subscription.ID), subscription.SecretCiphertext)
}

func (s *Service) Webhooks(ctx context.Context, workspaceID string) ([]*models.WebhookSubscription, error) {
	subscriptions, err := s.store.ListWebhookSubscriptions(ctx, workspaceID, "", false)
	if err != nil {
		return nil, err
	}
	for index, subscription := range subscriptions {
		subscriptions[index] = publicWebhookSubscription(subscription)
	}
	return subscriptions, nil
}

func (s *Service) SetWebhookEnabled(ctx context.Context, workspaceID, subscriptionID string, enabled bool) error {
	if workspaceID == "" || subscriptionID == "" {
		return integrationError(ErrInvalidInput, "", "webhooks", "Workspace and subscription IDs are required.")
	}
	return s.store.SetWebhookSubscriptionEnabled(ctx, workspaceID, subscriptionID, enabled)
}

func (s *Service) EmitWebhook(ctx context.Context, workspaceID, eventType, eventID string, data map[string]any) (int, error) {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if workspaceID == "" || !validWebhookEvent(eventType, false) {
		return 0, integrationError(ErrInvalidInput, "", "webhooks", "A workspace and valid webhook event type are required.")
	}
	if eventID == "" {
		eventID = uuid.NewString()
	}
	if data == nil {
		data = make(map[string]any)
	}
	now := s.nowUTC()
	envelope := WebhookEvent{ID: eventID, Type: eventType, WorkspaceID: workspaceID, CreatedAt: now, Data: data}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return 0, integrationError(ErrInvalidInput, "", "webhooks", "The webhook event data must be JSON serializable.")
	}
	subscriptions, err := s.store.ListWebhookSubscriptions(ctx, workspaceID, eventType, true)
	if err != nil {
		return 0, err
	}
	queued := 0
	var errs []error
	for _, subscription := range subscriptions {
		delivery := &models.WebhookDelivery{
			ID: uuid.NewString(), WorkspaceID: workspaceID, SubscriptionID: subscription.ID,
			EventID: eventID, EventType: eventType, PayloadJSON: string(payload),
			Status: models.WebhookPending, MaxAttempts: s.opts.WebhookMaxAttempts,
			NextAttemptAt: now, IdempotencyKey: eventID, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.EnqueueWebhookDelivery(ctx, delivery); err != nil {
			errs = append(errs, err)
			continue
		}
		queued++
	}
	return queued, errors.Join(errs...)
}

func (s *Service) WebhookDeliveries(ctx context.Context, workspaceID, subscriptionID string, limit int64) ([]*models.WebhookDelivery, error) {
	if limit == 0 {
		limit = 50
	}
	return s.store.ListWebhookDeliveries(ctx, workspaceID, subscriptionID, limit)
}

func (s *Service) SigningSecretResolver() func(*models.WebhookSubscription) (string, error) {
	return s.WebhookSecret
}

func (s *Service) WebhookTimestamp() time.Time {
	return s.nowUTC()
}
