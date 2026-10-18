package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStripeProviderCheckoutRetrieveAndRefund(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_mock" {
			t.Errorf("authorization header was not set correctly")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			if r.Header.Get("Idempotency-Key") != "checkout-purchase" {
				t.Errorf("missing checkout idempotency key: %s", r.Header.Get("Idempotency-Key"))
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			for key, expected := range map[string]string{
				"mode": "payment", "client_reference_id": "purchase", "customer_email": "reader@example.com",
				"line_items[0][price_data][unit_amount]": "1250", "line_items[0][price_data][currency]": "usd",
				"payment_intent_data[metadata][purchase_id]": "purchase",
			} {
				if r.Form.Get(key) != expected {
					t.Errorf("form %s = %q, want %q", key, r.Form.Get(key), expected)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"id":"cs_test","url":%q}`, server.URL+"/hosted")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_test":
			_ = json.NewEncoder(w).Encode(CheckoutSession{ID: "cs_test", PaymentIntentID: "pi_test", PaymentStatus: "paid"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			if r.Header.Get("Idempotency-Key") != "refund-purchase" {
				t.Errorf("missing refund idempotency key")
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("payment_intent") != "pi_test" || r.Form.Get("metadata[purchase_id]") != "purchase" {
				t.Errorf("incorrect refund form: %+v", r.Form)
			}
			_, _ = w.Write([]byte(`{"id":"re_test","status":"succeeded"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewStripeProvider("sk_test_mock", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.CreateCheckoutSession(context.Background(), CheckoutSessionInput{
		PurchaseID: "purchase", Email: "reader@example.com", AmountCents: 1250, Currency: "USD",
		SuccessURL: "https://flipbook.test/success?session_id={CHECKOUT_SESSION_ID}", CancelURL: "https://flipbook.test/cancel",
		Idempotency: "checkout-purchase",
	})
	if err != nil || session.ID != "cs_test" || !validHostedCheckoutURL(session.URL) {
		t.Fatalf("checkout session = %+v, %v", session, err)
	}
	retrieved, err := provider.RetrieveCheckoutSession(context.Background(), "cs_test")
	if err != nil || retrieved.PaymentIntentID != "pi_test" {
		t.Fatalf("retrieved session = %+v, %v", retrieved, err)
	}
	refund, err := provider.CreateRefund(context.Background(), RefundInput{
		PaymentIntentID: "pi_test", PurchaseID: "purchase", Idempotency: "refund-purchase",
	})
	if err != nil || refund.Status != "succeeded" {
		t.Fatalf("refund = %+v, %v", refund, err)
	}
}

func TestStripeProviderSanitizesErrorsAndRejectsInsecureBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"card declined","code":"card_declined"}}`))
	}))
	defer server.Close()
	provider, err := NewStripeProvider("sk_test_mock", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.RetrieveCheckoutSession(context.Background(), "cs_test")
	if !errors.Is(err, ErrProvider) || strings.Contains(err.Error(), "sk_test_mock") || !strings.Contains(err.Error(), "card declined") {
		t.Fatalf("provider error = %v", err)
	}
	if _, err := NewStripeProvider("sk_test_mock", "http://payments.example.com", nil); err == nil {
		t.Fatal("insecure provider URL accepted")
	}
}
