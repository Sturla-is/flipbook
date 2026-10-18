package sales

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func webhookPayload(t *testing.T, id, eventType string, object any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id": id, "type": eventType, "data": map[string]any{"object": object},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func signTestWebhook(payload []byte, secret string, now time.Time) string {
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "." + string(payload)))
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"id":"evt_test"}`)
	secret := "whsec_test"
	valid := signTestWebhook(payload, secret, now)
	tests := []struct {
		name      string
		header    string
		at        time.Time
		expectErr bool
	}{
		{name: "valid", header: valid, at: now},
		{name: "multiple signatures", header: valid + ",v1=" + valid[len(valid)-64:], at: now},
		{name: "wrong signature", header: "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=" + fmt.Sprintf("%064d", 0), at: now, expectErr: true},
		{name: "stale", header: valid, at: now.Add(10 * time.Minute), expectErr: true},
		{name: "future", header: valid, at: now.Add(-10 * time.Minute), expectErr: true},
		{name: "malformed", header: "not-a-signature", at: now, expectErr: true},
		{name: "empty", header: "", at: now, expectErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := VerifyWebhookSignature(payload, test.header, secret, test.at, 5*time.Minute)
			if (err != nil) != test.expectErr || (test.expectErr && !errors.Is(err, ErrSignature)) {
				t.Fatalf("signature error = %v", err)
			}
		})
	}
	if err := VerifyWebhookSignature(payload, valid, "", now, time.Minute); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing secret error = %v", err)
	}
}

func TestCurrencyParsingAndFormatting(t *testing.T) {
	for _, test := range []struct {
		value, currency string
		amount          int64
		formatted       string
	}{
		{value: "12.50", currency: "USD", amount: 1250, formatted: "12.50"},
		{value: "12.5", currency: "eur", amount: 1250, formatted: "12.50"},
		{value: "1250", currency: "JPY", amount: 1250, formatted: "1250"},
		{value: "0.01", currency: "GBP", amount: 1, formatted: "0.01"},
	} {
		currency, err := NormalizeCurrency(test.currency)
		if err != nil {
			t.Fatal(err)
		}
		amount, err := ParseAmount(test.value, currency)
		if err != nil || amount != test.amount || FormatAmount(amount, currency) != test.formatted {
			t.Fatalf("amount round trip = %d, %q, %v", amount, FormatAmount(amount, currency), err)
		}
	}
	for _, value := range []string{"", "-1", "+1", "1.234", "1,00", "abc"} {
		if _, err := ParseAmount(value, "USD"); err == nil {
			t.Fatalf("invalid amount %q accepted", value)
		}
	}
	if _, err := ParseAmount("10.0", "JPY"); err == nil {
		t.Fatal("fractional JPY amount accepted")
	}
	if got := FormatAmount(123456, "USD"); got != "1234.56" {
		t.Fatalf("formatted amount = %s", got)
	}
	if got := fmt.Sprintf("%s %s", FormatAmount(999, "EUR"), "EUR"); got != "9.99 EUR" {
		t.Fatalf("display amount = %s", got)
	}
}
