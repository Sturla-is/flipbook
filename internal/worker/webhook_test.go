package worker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
)

func TestWebhookDeliveryIsSignedAndRetriesFailures(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	worker := testWorker(t, db, store, &fakeConverter{})
	worker.opts.Webhooks.Enabled = true
	worker.opts.Now = func() time.Time { return time.Unix(1234, 0).UTC() }
	payload := `{"event":"conversion.ready"}`
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if string(body) != payload || request.Header.Get("X-Flipbook-Event") != "conversion.ready" ||
			request.Header.Get("X-Flipbook-Timestamp") != "1234" || request.Header.Get("Idempotency-Key") != "event-1" {
			t.Errorf("incorrect webhook request: body=%s headers=%v", body, request.Header)
		}
		mac := hmac.New(sha256.New, []byte("subscription-secret"))
		_, _ = io.WriteString(mac, "1234."+payload)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if request.Header.Get("X-Flipbook-Signature") != expected {
			t.Errorf("signature = %s, want %s", request.Header.Get("X-Flipbook-Signature"), expected)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	db.subs["subscription"] = &models.WebhookSubscription{
		ID: "subscription", WorkspaceID: "workspace", URL: server.URL,
		SecretCiphertext: "subscription-secret", Events: []string{"conversion.ready"}, Enabled: true,
	}
	delivery := &models.WebhookDelivery{
		ID: "delivery", WorkspaceID: "workspace", SubscriptionID: "subscription", EventID: "event-1",
		EventType: "conversion.ready", PayloadJSON: payload, Status: models.WebhookDelivering,
		Attempts: 1, MaxAttempts: 2, LeaseOwner: worker.opts.WorkerID,
	}
	db.deliveries[delivery.ID] = delivery
	if err := worker.processLeasedWebhook(context.Background(), delivery); err != nil {
		t.Fatalf("deliver webhook: %v", err)
	}
	if requests != 1 || delivery.Status != models.WebhookDelivered || delivery.ResponseStatus != http.StatusNoContent {
		t.Fatalf("webhook was not delivered: requests=%d delivery=%+v", requests, delivery)
	}

	failureServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failureServer.Close()
	db.subs["subscription"].URL = failureServer.URL
	failed := &models.WebhookDelivery{
		ID: "failed-delivery", WorkspaceID: "workspace", SubscriptionID: "subscription", EventID: "event-2",
		EventType: "conversion.ready", PayloadJSON: payload, Status: models.WebhookDelivering,
		Attempts: 1, MaxAttempts: 2, LeaseOwner: worker.opts.WorkerID,
	}
	db.deliveries[failed.ID] = failed
	if err := worker.processLeasedWebhook(context.Background(), failed); err == nil {
		t.Fatal("expected webhook failure")
	}
	if failed.Status != models.WebhookRetry || failed.ResponseStatus != http.StatusServiceUnavailable || failed.LastError == "" {
		t.Fatalf("webhook failure was not observable/retryable: %+v", failed)
	}
}

func TestWebhookSecretResolverAndDisabledSubscription(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	worker := testWorker(t, db, store, &fakeConverter{})
	worker.opts.Webhooks.Enabled = true
	resolverCalled := false
	worker.opts.WebhookSecret = func(subscription *models.WebhookSubscription) (string, error) {
		resolverCalled = true
		return "resolved", nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mac := hmac.New(sha256.New, []byte("resolved"))
		_, _ = io.WriteString(mac, request.Header.Get("X-Flipbook-Timestamp")+`.{"ok":true}`)
		if request.Header.Get("X-Flipbook-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("resolved secret was not used")
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	db.subs["subscription"] = &models.WebhookSubscription{
		ID: "subscription", WorkspaceID: "workspace", URL: server.URL, Events: []string{"*"}, Enabled: true,
	}
	delivery := &models.WebhookDelivery{
		ID: "delivery", WorkspaceID: "workspace", SubscriptionID: "subscription", EventID: "event",
		EventType: "publication.published", PayloadJSON: `{"ok":true}`, Status: models.WebhookDelivering,
		Attempts: 1, MaxAttempts: 2, LeaseOwner: worker.opts.WorkerID,
	}
	db.deliveries[delivery.ID] = delivery
	if err := worker.processLeasedWebhook(context.Background(), delivery); err != nil || !resolverCalled {
		t.Fatalf("resolver delivery failed: called=%v err=%v", resolverCalled, err)
	}

	db.subs["subscription"].Enabled = false
	disabled := &models.WebhookDelivery{
		ID: "disabled", WorkspaceID: "workspace", SubscriptionID: "subscription", EventID: "event-3",
		EventType: "publication.published", PayloadJSON: `{"ok":true}`, Status: models.WebhookDelivering,
		Attempts: 1, MaxAttempts: 2, LeaseOwner: worker.opts.WorkerID,
	}
	db.deliveries[disabled.ID] = disabled
	if err := worker.processLeasedWebhook(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.Status != models.WebhookDelivered || disabled.ResponseStatus != 0 {
		t.Fatalf("disabled subscription was not acknowledged: %+v", disabled)
	}
}
