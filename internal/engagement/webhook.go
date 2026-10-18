package engagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
)

const (
	EventLeadCaptured           = "lead.captured"
	EventSubscriberPending      = "subscriber.pending"
	EventSubscriberConfirmed    = "subscriber.confirmed"
	EventSubscriberUnsubscribed = "subscriber.unsubscribed"
	EventPublicationPublished   = "publication.published"
)

var webhookEventPattern = regexp.MustCompile(`^([a-z][a-z0-9]*([._-][a-z0-9]+)*|\*)$`)

type WebhookInput struct {
	WorkspaceID string
	URL         string
	Events      []string
	Secret      string
}

type WebhookResult struct {
	Subscription *models.WebhookSubscription
	Secret       string
}

func (s *Service) CreateWebhook(ctx context.Context, input WebhookInput) (*WebhookResult, error) {
	if !s.webhooks.Enabled {
		return nil, fmt.Errorf("%w: generic webhooks are disabled", ErrNotConfigured)
	}
	if strings.TrimSpace(input.WorkspaceID) == "" {
		return nil, fmt.Errorf("%w: webhook workspace is required", ErrInvalidInput)
	}
	endpoint, err := validateWebhookURL(input.URL)
	if err != nil {
		return nil, err
	}
	events, err := validateWebhookEvents(input.Events)
	if err != nil {
		return nil, err
	}
	id, err := randomURLToken(s.random, 16)
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(input.Secret)
	generated := false
	if secret == "" && s.webhooks.SigningSecret == "" {
		secret, err = randomURLToken(s.random, 32)
		generated = true
		if err != nil {
			return nil, err
		}
	}
	subscription := &models.WebhookSubscription{
		ID: id, WorkspaceID: input.WorkspaceID, URL: endpoint, Events: events,
		Enabled: true, CreatedAt: s.timeNow(),
	}
	returnedSecret := ""
	if secret != "" {
		if len(secret) < 32 || len(secret) > 4096 || strings.ContainsAny(secret, "\r\n\x00") {
			return nil, fmt.Errorf("%w: webhook secrets must contain 32 to 4096 bytes", ErrInvalidInput)
		}
		ciphertext, err := s.sealWebhookSecret(id, secret)
		if err != nil {
			return nil, err
		}
		subscription.SecretCiphertext = ciphertext
		if generated || strings.TrimSpace(input.Secret) != "" {
			returnedSecret = secret
		}
	}
	if err := s.store.CreateWebhookSubscription(ctx, subscription); err != nil {
		return nil, err
	}
	return &WebhookResult{Subscription: subscription, Secret: returnedSecret}, nil
}

func validateWebhookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", fmt.Errorf("%w: a webhook URL is required", ErrInvalidInput)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: webhook URL must not contain credentials, query, or fragment", ErrInvalidInput)
	}
	if parsed.Scheme != "https" {
		loopback := parsed.Scheme == "http" && (strings.EqualFold(parsed.Hostname(), "localhost") || (net.ParseIP(parsed.Hostname()) != nil && net.ParseIP(parsed.Hostname()).IsLoopback()))
		if !loopback {
			return "", fmt.Errorf("%w: webhook URLs must use HTTPS except on loopback", ErrInvalidInput)
		}
	}
	return parsed.String(), nil
}

func validateWebhookEvents(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > 20 {
		return nil, fmt.Errorf("%w: select between one and 20 webhook events", ErrInvalidInput)
	}
	seen := make(map[string]bool, len(input))
	events := make([]string, 0, len(input))
	for _, event := range input {
		event = strings.ToLower(strings.TrimSpace(event))
		if len(event) > 100 || !webhookEventPattern.MatchString(event) {
			return nil, fmt.Errorf("%w: invalid webhook event name", ErrInvalidInput)
		}
		if !seen[event] {
			seen[event] = true
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *Service) SetWebhookEnabled(ctx context.Context, workspaceID, subscriptionID string, enabled bool) error {
	if enabled && !s.webhooks.Enabled {
		return fmt.Errorf("%w: generic webhooks are disabled", ErrNotConfigured)
	}
	return s.store.SetWebhookSubscriptionEnabled(ctx, workspaceID, subscriptionID, enabled)
}

func (s *Service) Webhooks(ctx context.Context, workspaceID string) ([]*models.WebhookSubscription, error) {
	return s.store.ListWebhookSubscriptions(ctx, workspaceID, "", false)
}

func (s *Service) WebhookDeliveries(ctx context.Context, workspaceID string, limit int64) ([]*models.WebhookDelivery, error) {
	if limit == 0 {
		limit = 100
	}
	return s.store.ListWebhookDeliveries(ctx, workspaceID, "", limit)
}

func (s *Service) ResolveWebhookSecret(subscription *models.WebhookSubscription) (string, error) {
	if subscription == nil {
		return "", errors.New("webhook subscription is required")
	}
	if subscription.SecretCiphertext == "" {
		if s.webhooks.SigningSecret == "" {
			return "", fmt.Errorf("%w: webhook signing secret", ErrNotConfigured)
		}
		return s.webhooks.SigningSecret, nil
	}
	return s.openWebhookSecret(subscription.ID, subscription.SecretCiphertext)
}

func (s *Service) emit(ctx context.Context, workspaceID, eventType string, data map[string]any) error {
	if !s.webhooks.Enabled {
		return nil
	}
	if _, err := validateWebhookEvents([]string{eventType}); err != nil {
		return err
	}
	subscriptions, err := s.store.ListWebhookSubscriptions(ctx, workspaceID, eventType, true)
	if err != nil || len(subscriptions) == 0 {
		return err
	}
	eventID, err := randomURLToken(s.random, 16)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": eventID, "type": eventType,
		"source": s.baseURL, "time": s.timeNow().Format(time.RFC3339Nano),
		"workspace_id": workspaceID, "data": data,
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, subscription := range subscriptions {
		if subscription.SecretCiphertext == "" && s.webhooks.SigningSecret == "" {
			errs = append(errs, fmt.Errorf("%w: webhook signing secret", ErrNotConfigured))
			continue
		}
		delivery := &models.WebhookDelivery{
			WorkspaceID: workspaceID, SubscriptionID: subscription.ID, EventID: eventID,
			EventType: eventType, PayloadJSON: string(payload), Status: models.WebhookPending,
			MaxAttempts: s.webhooks.MaxAttempts, NextAttemptAt: s.timeNow(),
			IdempotencyKey: eventID, CreatedAt: s.timeNow(),
		}
		if err := s.store.EnqueueWebhookDelivery(ctx, delivery); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
