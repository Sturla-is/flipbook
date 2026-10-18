package engagement

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/metavert-io/flipbook/internal/config"
	"github.com/metavert-io/flipbook/internal/models"
)

func TestCRMConnectorsUseMockedHTTP(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") == "" && r.URL.Path != "/services/data/v60.0/query" {
			t.Error("provider credential was not sent")
		}
		switch r.URL.Path {
		case "/crm/v3/objects/contacts/batch/upsert":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "reader@example.com") {
				t.Errorf("HubSpot body missing email: %s", body)
			}
			w.WriteHeader(http.StatusOK)
		case "/services/data/v60.0/query":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[]}`))
		case "/services/data/v60.0/sobjects/Lead/":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"Company":"Flipbook"`) {
				t.Errorf("Salesforce body missing required company: %s", body)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			if strings.HasPrefix(r.URL.Path, "/lists/audience/members/") {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "reader@example.com") {
					t.Errorf("Mailchimp body missing email: %s", body)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	providerConfig := config.CRMProviderConfig{Enabled: true, APIKey: "test-secret", InstanceURL: server.URL, ListID: "audience"}
	dispatcher := NewCRMDispatcher(config.CRMConfig{HubSpot: providerConfig, Salesforce: providerConfig, Mailchimp: providerConfig}, server.Client())
	lead := &models.Lead{Email: "reader@example.com", NormalizedEmail: "reader@example.com", Name: "Example Reader"}
	results := dispatcher.DeliverLead(context.Background(), lead)
	if len(results) != 3 {
		t.Fatalf("delivery results = %+v", results)
	}
	for _, result := range results {
		if !result.Delivered || result.Error != "" {
			t.Fatalf("provider delivery failed: %+v", result)
		}
	}
	subscriber := &models.Subscriber{Email: "reader@example.com", NormalizedEmail: "reader@example.com", Status: models.SubscriberSubscribed}
	results = dispatcher.DeliverSubscriber(context.Background(), subscriber)
	if len(results) != 1 || !results[0].Delivered || results[0].Provider != ProviderMailchimp {
		t.Fatalf("subscriber delivery = %+v", results)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 {
		t.Fatalf("HTTP request count = %d, want 5 (%v)", len(requests), requests)
	}
	for _, status := range dispatcher.Status() {
		if !status.Configured || !status.Verified {
			t.Fatalf("provider was not configured and verified: %+v", status)
		}
		if status.LastError != "" {
			t.Fatalf("unexpected provider status error: %+v", status)
		}
	}
}

func TestCRMFailuresAreSanitized(t *testing.T) {
	const secret = "do-not-expose-this-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()
	dispatcher := NewCRMDispatcher(config.CRMConfig{HubSpot: config.CRMProviderConfig{
		Enabled: true, AccessToken: secret, InstanceURL: server.URL,
	}}, server.Client())
	results := dispatcher.DeliverLead(context.Background(), &models.Lead{Email: "reader@example.com", NormalizedEmail: "reader@example.com"})
	encoded, _ := json.Marshal(results)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("provider response or credential leaked in result: %s", encoded)
	}
	for _, status := range dispatcher.Status() {
		if strings.Contains(status.LastError, secret) {
			t.Fatalf("provider secret leaked in status: %+v", status)
		}
	}
}

func TestSMTPDisabledAndHeaderValidation(t *testing.T) {
	mailer := NewSMTPMailer(config.SMTPConfig{})
	if mailer.Enabled() {
		t.Fatal("empty SMTP configuration was enabled")
	}
	mailer = NewSMTPMailer(config.SMTPConfig{Enabled: true, Host: "localhost", Port: 25, From: "sender@example.com", FromName: "Flipbook", TLSMode: "none"})
	_, err := mailer.message("sender@example.com", "reader@example.com", Message{
		To: "reader@example.com", Subject: "Safe subject", Text: "Body", Headers: map[string]string{"X-Test": "bad\r\nInjected: true"},
	})
	if err == nil {
		t.Fatal("header injection was accepted")
	}
}
