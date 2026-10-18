package engagement

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/metavert-io/flipbook/internal/models"
)

func TestSubscriptionDoubleOptInNotificationAndUnsubscribe(t *testing.T) {
	service, store, _, mailer, fb := engagementFixture(t)
	result, err := service.Subscribe(context.Background(), SubscribeInput{
		WorkspaceID: fb.WorkspaceID, Email: "Reader@Example.com", Consent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.SubscriberPending || len(mailer.messages) != 1 {
		t.Fatalf("subscription did not enter pending state: %+v", result)
	}
	confirmationURL := messageLink(mailer.messages[0].Text, "Confirm subscription: ")
	parsed, err := url.Parse(confirmationURL)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("token")
	if token == "" || parsed.Query().Get("workspace_id") != fb.WorkspaceID {
		t.Fatalf("invalid confirmation URL: %s", confirmationURL)
	}
	stored, err := store.GetSubscriber(context.Background(), fb.WorkspaceID, "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.SubscriberPending || stored.ConfirmationTokenHash == token || stored.UnsubscribeTokenHash == "" {
		t.Fatalf("raw tokens were stored or subscription was confirmed early: %+v", stored)
	}
	if _, err := service.ConfirmSubscription(context.Background(), fb.WorkspaceID, token); err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.SubscriberSubscribed {
		t.Fatalf("subscriber status = %s", stored.Status)
	}
	report, err := service.NotifyNewPublication(context.Background(), fb.WorkspaceID, fb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Sent != 1 || report.Failed != 0 || len(mailer.messages) != 2 {
		t.Fatalf("unexpected notification report: %+v", report)
	}
	notification := mailer.messages[1]
	if !strings.Contains(notification.Text, "https://flipbook.example/v/a-guide") || notification.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("notification missing viewer or one-click links: %+v", notification)
	}
	unsubscribeURL, err := url.Parse(strings.Trim(notification.Headers["List-Unsubscribe"], "<>"))
	if err != nil {
		t.Fatal(err)
	}
	unsubscribeToken := unsubscribeURL.Query().Get("token")
	if _, err := service.Unsubscribe(context.Background(), fb.WorkspaceID, unsubscribeToken); err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.SubscriberUnsubscribed {
		t.Fatalf("subscriber status = %s, want unsubscribed", stored.Status)
	}
}

func TestSubscriptionRequiresSMTPOnlyForNewDelivery(t *testing.T) {
	service, _, _, mailer, fb := engagementFixture(t)
	mailer.enabled = false
	_, err := service.Subscribe(context.Background(), SubscribeInput{WorkspaceID: fb.WorkspaceID, Email: "reader@example.com", Consent: true})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want SMTP not configured", err)
	}
	report, err := service.NotifyNewPublication(context.Background(), fb.WorkspaceID, fb.ID)
	if err != nil || report.Total != 0 {
		t.Fatalf("zero-subscriber publication notification should not require SMTP: %+v, %v", report, err)
	}
}

func TestInvalidSubscriptionTokenIsRejected(t *testing.T) {
	service, _, _, _, fb := engagementFixture(t)
	if _, err := service.ConfirmSubscription(context.Background(), fb.WorkspaceID, "short"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want invalid token", err)
	}
}

func messageLink(body, prefix string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
