package engagement

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/metavert-io/flipbook/internal/integrations"
	"github.com/metavert-io/flipbook/internal/models"
)

func TestWebhookSecretEncryptionAndResolver(t *testing.T) {
	service, _, _, _, fb := engagementFixture(t)
	secret := strings.Repeat("z", 32)
	result, err := service.CreateWebhook(context.Background(), WebhookInput{
		WorkspaceID: fb.WorkspaceID, URL: "https://hooks.example/engagement", Events: []string{EventLeadCaptured, EventPublicationPublished}, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Secret != secret || result.Subscription.SecretCiphertext == "" || result.Subscription.SecretCiphertext == secret {
		t.Fatalf("webhook secret was not returned once and encrypted: %+v", result.Subscription)
	}
	resolved, err := service.ResolveWebhookSecret(result.Subscription)
	if err != nil || resolved != secret {
		t.Fatalf("resolved secret mismatch: %v", err)
	}
	cipher, err := integrations.NewTokenCipher(strings.Repeat("e", 32))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = cipher.Open("integration-webhook/"+result.Subscription.ID+"/signing-secret", result.Subscription.SecretCiphertext)
	if err != nil || resolved != secret {
		t.Fatalf("shared integration resolver could not decrypt secret: %v", err)
	}
	other := *result.Subscription
	other.ID = "other-id"
	if _, err := service.ResolveWebhookSecret(&other); err == nil {
		t.Fatal("ciphertext was not bound to the subscription ID")
	}
}

func TestWebhookValidationAndGeneration(t *testing.T) {
	service, _, _, _, fb := engagementFixture(t)
	if _, err := service.CreateWebhook(context.Background(), WebhookInput{
		WorkspaceID: fb.WorkspaceID, URL: "http://hooks.example/insecure", Events: []string{EventLeadCaptured},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("insecure webhook error = %v", err)
	}
	result, err := service.CreateWebhook(context.Background(), WebhookInput{
		WorkspaceID: fb.WorkspaceID, URL: "http://127.0.0.1:8080/hook", Events: []string{EventLeadCaptured},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Secret != "" || result.Subscription.SecretCiphertext != "" {
		t.Fatal("deployment signing secret should be used without generating an override")
	}
	service.webhooks.SigningSecret = ""
	generated, err := service.CreateWebhook(context.Background(), WebhookInput{
		WorkspaceID: fb.WorkspaceID, URL: "https://hooks.example/generated", Events: []string{"*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Secret) < 32 {
		t.Fatal("missing generated webhook secret")
	}
	resolved, err := service.ResolveWebhookSecret(generated.Subscription)
	if err != nil || resolved != generated.Secret {
		t.Fatalf("generated secret could not be resolved: %v", err)
	}
}

func TestWebhookEventPayloadDoesNotContainSubscriptionSecret(t *testing.T) {
	service, store, _, _, fb := engagementFixture(t)
	secret := strings.Repeat("q", 32)
	store.webhooks["encrypted"] = &models.WebhookSubscription{
		ID: "encrypted", WorkspaceID: fb.WorkspaceID, URL: "https://hooks.example", Events: []string{"*"}, Enabled: true, SecretCiphertext: secret,
	}
	if err := service.emit(context.Background(), fb.WorkspaceID, EventPublicationPublished, map[string]any{"flipbook_id": fb.ID}); err != nil {
		t.Fatal(err)
	}
	for _, delivery := range store.deliveries {
		if strings.Contains(delivery.PayloadJSON, secret) {
			t.Fatal("subscription secret leaked into event payload")
		}
	}
}
