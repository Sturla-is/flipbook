package engagement

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type SubscribeInput struct {
	WorkspaceID string
	Email       string
	Consent     bool
	Source      string
}

type SubscribeResult struct {
	Status            models.SubscriberStatus
	AlreadySubscribed bool
	Delivery          []DeliveryResult
}

type SubscriptionResult struct {
	Subscriber *models.Subscriber
	Delivery   []DeliveryResult
}

type NotificationReport struct {
	Total    int
	Sent     int
	Failed   int
	Delivery []DeliveryResult
}

func (s *Service) Subscribe(ctx context.Context, input SubscribeInput) (*SubscribeResult, error) {
	email, err := NormalizeEmail(input.Email)
	if err != nil {
		return nil, err
	}
	if !input.Consent {
		return nil, ErrConsentRequired
	}
	workspace, err := s.store.GetWorkspace(ctx, input.WorkspaceID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	existing, err := s.store.GetSubscriber(ctx, input.WorkspaceID, email)
	if err == nil && existing.Status == models.SubscriberSubscribed {
		return &SubscribeResult{Status: models.SubscriberSubscribed, AlreadySubscribed: true}, nil
	}
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}
	if s.mailer == nil || !s.mailer.Enabled() {
		return nil, fmt.Errorf("%w: SMTP delivery", ErrNotConfigured)
	}
	confirmationToken, err := randomURLToken(s.random, 32)
	if err != nil {
		return nil, err
	}
	now := s.timeNow()
	expires := now.Add(s.confirmationTTL)
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "catalog"
	}
	if len(source) > 100 {
		return nil, fmt.Errorf("%w: source is too long", ErrInvalidInput)
	}
	subscriber := &models.Subscriber{
		WorkspaceID: input.WorkspaceID, Email: email, Status: models.SubscriberPending,
		Source: source, ConfirmationTokenHash: models.HashToken(confirmationToken),
		UnsubscribeTokenHash:  models.HashToken(s.unsubscribeToken(input.WorkspaceID, email)),
		ConfirmationExpiresAt: &expires, CreatedAt: now,
	}
	if err := s.store.UpsertSubscriber(ctx, subscriber); err != nil {
		return nil, err
	}
	result := &SubscribeResult{Status: models.SubscriberPending}
	confirmURL := s.subscriptionURL("/subscriptions/confirm", input.WorkspaceID, confirmationToken)
	unsubscribeURL := s.subscriptionURL("/subscriptions/unsubscribe", input.WorkspaceID, subscriber.Email)
	workspaceName := strings.TrimSpace(workspace.Name)
	if workspaceName == "" {
		workspaceName = "Penninn Midlun"
	}
	message := Message{
		To: subscriber.Email, Subject: "Confirm your subscription to " + workspaceName,
		Text: "Confirm your subscription to receive new publication notifications from " + workspaceName + ".\n\n" +
			"Confirm subscription: " + confirmURL + "\n\nThis confirmation link expires in " + s.confirmationTTL.String() + ". " +
			"If you did not request this subscription, ignore this message.\n\n" +
			"Unsubscribe: " + unsubscribeURL + "\n",
		Headers: unsubscribeHeaders(unsubscribeURL),
	}
	if err := s.mailer.Send(ctx, message); err != nil {
		return nil, fmt.Errorf("%w: confirmation email could not be sent", ErrDelivery)
	}
	if err := s.emit(ctx, input.WorkspaceID, EventSubscriberPending, map[string]any{
		"email": subscriber.Email, "source": subscriber.Source, "confirmation_expires_at": expires,
	}); err != nil {
		result.Delivery = append(result.Delivery, DeliveryResult{Provider: "webhook", Error: "webhook could not be queued"})
	}
	return result, nil
}

func (s *Service) ConfirmSubscription(ctx context.Context, workspaceID, token string) (*SubscriptionResult, error) {
	if err := validateSubscriptionToken(token); err != nil {
		return nil, err
	}
	subscribers, err := s.store.ListSubscribers(ctx, workspaceID, models.SubscriberPending)
	if err != nil {
		return nil, err
	}
	hash := models.HashToken(token)
	var subscriber *models.Subscriber
	for _, candidate := range subscribers {
		if hmac.Equal([]byte(candidate.ConfirmationTokenHash), []byte(hash)) {
			subscriber = candidate
			break
		}
	}
	if subscriber == nil || subscriber.ConfirmationExpiresAt == nil || !s.timeNow().Before(*subscriber.ConfirmationExpiresAt) {
		return nil, ErrInvalidToken
	}
	if err := s.store.ConfirmSubscriber(ctx, token); err != nil {
		return nil, translateTokenError(err)
	}
	now := s.timeNow()
	subscriber.Status = models.SubscriberSubscribed
	subscriber.ConfirmedAt = &now
	subscriber.ConfirmationTokenHash = ""
	subscriber.ConfirmationExpiresAt = nil
	result := &SubscriptionResult{Subscriber: subscriber}
	result.Delivery = s.crm.DeliverSubscriber(ctx, subscriber)
	if err := s.emit(ctx, workspaceID, EventSubscriberConfirmed, map[string]any{
		"email": subscriber.Email, "confirmed_at": now,
	}); err != nil {
		result.Delivery = append(result.Delivery, DeliveryResult{Provider: "webhook", Error: "webhook could not be queued"})
	}
	return result, nil
}

func (s *Service) Unsubscribe(ctx context.Context, workspaceID, token string) (*SubscriptionResult, error) {
	if err := validateSubscriptionToken(token); err != nil {
		return nil, err
	}
	subscribers, err := s.store.ListSubscribers(ctx, workspaceID, "")
	if err != nil {
		return nil, err
	}
	hash := models.HashToken(token)
	var subscriber *models.Subscriber
	for _, candidate := range subscribers {
		if hmac.Equal([]byte(candidate.UnsubscribeTokenHash), []byte(hash)) {
			subscriber = candidate
			break
		}
	}
	if subscriber == nil {
		return nil, ErrInvalidToken
	}
	if subscriber.Status == models.SubscriberUnsubscribed {
		return &SubscriptionResult{Subscriber: subscriber}, nil
	}
	if err := s.store.UnsubscribeSubscriber(ctx, token); err != nil {
		return nil, translateTokenError(err)
	}
	now := s.timeNow()
	subscriber.Status = models.SubscriberUnsubscribed
	subscriber.UnsubscribedAt = &now
	result := &SubscriptionResult{Subscriber: subscriber}
	result.Delivery = s.crm.DeliverSubscriber(ctx, subscriber)
	if err := s.emit(ctx, workspaceID, EventSubscriberUnsubscribed, map[string]any{
		"email": subscriber.Email, "unsubscribed_at": now,
	}); err != nil {
		result.Delivery = append(result.Delivery, DeliveryResult{Provider: "webhook", Error: "webhook could not be queued"})
	}
	return result, nil
}

func validateSubscriptionToken(token string) error {
	if len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, "\r\n\x00") {
		return ErrInvalidToken
	}
	return nil
}

func translateTokenError(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrInvalidToken
	}
	return err
}

func (s *Service) Subscribers(ctx context.Context, workspaceID string) ([]*models.Subscriber, error) {
	return s.store.ListSubscribers(ctx, workspaceID, "")
}

func (s *Service) DeleteSubscriber(ctx context.Context, workspaceID, subscriberID string) error {
	if strings.TrimSpace(subscriberID) == "" {
		return fmt.Errorf("%w: subscriber id is required", ErrInvalidInput)
	}
	return s.store.DeleteSubscriber(ctx, workspaceID, subscriberID)
}

func (s *Service) NotifyNewPublication(ctx context.Context, workspaceID, flipbookID string) (*NotificationReport, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	if !fb.IsPublished(s.timeNow()) || fb.Visibility != models.VisibilityPublic {
		return nil, fmt.Errorf("%w: only currently public publications can trigger notifications", ErrInvalidInput)
	}
	subscribers, err := s.store.ListSubscribers(ctx, workspaceID, models.SubscriberSubscribed)
	if err != nil {
		return nil, err
	}
	report := &NotificationReport{Total: len(subscribers)}
	if err := s.emit(ctx, workspaceID, EventPublicationPublished, map[string]any{
		"flipbook_id": fb.ID, "slug": fb.Slug, "title": fb.Title, "published_at": fb.PublishedAt,
	}); err != nil {
		report.Delivery = append(report.Delivery, DeliveryResult{Provider: "webhook", Error: "webhook could not be queued"})
	}
	if len(subscribers) == 0 {
		return report, nil
	}
	if s.mailer == nil || !s.mailer.Enabled() {
		return report, fmt.Errorf("%w: SMTP delivery", ErrNotConfigured)
	}
	viewerURL := s.baseURL + "/v/" + url.PathEscape(fb.Slug)
	for _, subscriber := range subscribers {
		unsubscribeURL := s.subscriptionURL("/subscriptions/unsubscribe", workspaceID, subscriber.Email)
		message := Message{
			To: subscriber.Email, Subject: "New publication: " + fb.Title,
			Text: "A new publication is available: " + fb.Title + "\n\nRead it here: " + viewerURL + "\n\n" +
				"You receive these messages because you subscribed to new-publication notifications.\n" +
				"Unsubscribe: " + unsubscribeURL + "\n",
			Headers: unsubscribeHeaders(unsubscribeURL),
		}
		if err := s.mailer.Send(ctx, message); err != nil {
			report.Failed++
			report.Delivery = append(report.Delivery, DeliveryResult{Provider: "smtp", Error: "a notification could not be sent"})
			continue
		}
		report.Sent++
		report.Delivery = append(report.Delivery, DeliveryResult{Provider: "smtp", Delivered: true})
	}
	return report, nil
}

func (s *Service) subscriptionURL(path, workspaceID, emailOrToken string) string {
	values := url.Values{"workspace_id": {workspaceID}}
	if strings.Contains(emailOrToken, "@") {
		values.Set("token", s.unsubscribeToken(workspaceID, emailOrToken))
	} else {
		values.Set("token", emailOrToken)
	}
	return s.baseURL + path + "?" + values.Encode()
}

func unsubscribeHeaders(unsubscribeURL string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + unsubscribeURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

func validConfirmationTime(expires *time.Time, now time.Time) bool {
	return expires != nil && now.Before(*expires)
}
