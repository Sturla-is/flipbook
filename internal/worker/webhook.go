package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

func (w *Worker) webhookLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		deliveries, err := w.db.LeaseWebhookDeliveries(ctx, w.opts.WorkerID, 1, w.opts.LeaseDuration)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Printf("[worker] webhook lease failed: %v", err)
			}
			if !waitContext(ctx, w.opts.PollInterval) {
				return
			}
			continue
		}
		if len(deliveries) == 0 {
			if !waitContext(ctx, w.opts.PollInterval) {
				return
			}
			continue
		}
		for _, delivery := range deliveries {
			if err := w.processLeasedWebhook(ctx, delivery); err != nil && ctx.Err() == nil {
				w.logger.Printf("[worker] webhook %s failed: %v", delivery.ID, err)
			}
		}
	}
}

func (w *Worker) ProcessWebhookOne(ctx context.Context) (bool, error) {
	delivery, err := w.db.LeaseWebhookDeliveries(ctx, w.opts.WorkerID, 1, w.opts.LeaseDuration)
	if err != nil || len(delivery) == 0 {
		return false, err
	}
	return true, w.processLeasedWebhook(ctx, delivery[0])
}

func (w *Worker) processLeasedWebhook(ctx context.Context, delivery *models.WebhookDelivery) error {
	if delivery == nil {
		return errors.New("nil webhook delivery")
	}
	status, err := w.deliverWebhook(ctx, delivery)
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.CompletionTimeout)
	defer cancel()
	if err == nil {
		return w.db.CompleteWebhookDelivery(completionCtx, delivery.ID, w.opts.WorkerID, status)
	}
	if failErr := w.db.FailWebhookDelivery(
		completionCtx, delivery.ID, w.opts.WorkerID, boundedError(err), status,
		time.Duration(w.opts.Webhooks.RetryBackoffSeconds)*time.Second,
	); failErr != nil {
		return errors.Join(err, fmt.Errorf("record webhook failure: %w", failErr))
	}
	return err
}

func (w *Worker) deliverWebhook(ctx context.Context, delivery *models.WebhookDelivery) (int, error) {
	if !json.Valid([]byte(delivery.PayloadJSON)) {
		return 0, errors.New("webhook payload is not valid JSON")
	}
	subscriptions, err := w.db.ListWebhookSubscriptions(ctx, delivery.WorkspaceID, delivery.EventType, true)
	if err != nil {
		return 0, err
	}
	var subscription *models.WebhookSubscription
	for _, candidate := range subscriptions {
		if candidate.ID == delivery.SubscriptionID {
			subscription = candidate
			break
		}
	}
	if subscription == nil {
		return 0, nil
	}
	secret := w.opts.Webhooks.SigningSecret
	if secret == "" {
		secret = subscription.SecretCiphertext
	}
	if w.opts.WebhookSecret != nil {
		secret, err = w.opts.WebhookSecret(subscription)
		if err != nil {
			return 0, err
		}
	}
	if secret == "" {
		return 0, errors.New("webhook signing secret is not configured")
	}

	timeout := time.Duration(w.opts.Webhooks.TimeoutSeconds) * time.Second
	if timeout > w.opts.LeaseDuration/2 {
		timeout = w.opts.LeaseDuration / 2
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, subscription.URL, bytes.NewBufferString(delivery.PayloadJSON))
	if err != nil {
		return 0, err
	}
	timestamp := strconv.FormatInt(w.opts.Now().Unix(), 10)
	signedPayload := timestamp + "." + delivery.PayloadJSON
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, signedPayload)
	signature := hex.EncodeToString(mac.Sum(nil))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Flipbook-Webhooks/1.0")
	request.Header.Set("X-Flipbook-Event", delivery.EventType)
	request.Header.Set("X-Flipbook-Delivery", delivery.ID)
	request.Header.Set("X-Flipbook-Timestamp", timestamp)
	request.Header.Set("X-Flipbook-Signature", "sha256="+signature)
	idempotencyKey := delivery.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = delivery.EventID
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}

	client := w.opts.WebhookHTTPClient
	if client == nil {
		client = webhookHTTPClient(timeout)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	limit := w.opts.Webhooks.MaxResponseBodyBytes
	if limit > 0 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, limit))
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}
