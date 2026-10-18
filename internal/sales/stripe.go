package sales

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxProviderResponseBytes = 1 << 20

type StripeProvider struct {
	secret  string
	baseURL *url.URL
	client  HTTPClient
}

func NewStripeProvider(secret, baseURL string, client HTTPClient) (*StripeProvider, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrNotConfigured
	}
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, invalid("invalid payment provider API URL")
	}
	if parsed.Scheme != "https" && !loopbackHTTP(parsed) {
		return nil, invalid("payment provider API URL must use HTTPS")
	}
	if client == nil {
		client = &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &StripeProvider{secret: secret, baseURL: parsed, client: client}, nil
}

func loopbackHTTP(value *url.URL) bool {
	if value.Scheme != "http" {
		return false
	}
	host := value.Hostname()
	return host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func (p *StripeProvider) CreateCheckoutSession(ctx context.Context, input CheckoutSessionInput) (*CheckoutSession, error) {
	form := url.Values{
		"mode":                    {"payment"},
		"success_url":             {input.SuccessURL},
		"cancel_url":              {input.CancelURL},
		"client_reference_id":     {input.PurchaseID},
		"customer_email":          {input.Email},
		"line_items[0][quantity]": {"1"},
		"metadata[purchase_id]":   {input.PurchaseID},
		"metadata[workspace_id]":  {input.WorkspaceID},
		"metadata[flipbook_id]":   {input.FlipbookID},
		"payment_intent_data[metadata][purchase_id]":  {input.PurchaseID},
		"payment_intent_data[metadata][workspace_id]": {input.WorkspaceID},
		"payment_intent_data[metadata][flipbook_id]":  {input.FlipbookID},
	}
	if input.PriceID != "" {
		form.Set("line_items[0][price]", input.PriceID)
	} else {
		form.Set("line_items[0][price_data][currency]", strings.ToLower(input.Currency))
		form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", input.AmountCents))
		form.Set("line_items[0][price_data][product_data][name]", input.Title)
	}
	var session CheckoutSession
	if err := p.doJSON(ctx, http.MethodPost, "/v1/checkout/sessions", form, input.Idempotency, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (p *StripeProvider) RetrieveCheckoutSession(ctx context.Context, sessionID string) (*CheckoutSession, error) {
	if sessionID == "" {
		return nil, invalid("checkout session id is required")
	}
	var session CheckoutSession
	if err := p.doJSON(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(sessionID), nil, "", &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (p *StripeProvider) ExpireCheckoutSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return p.doJSON(ctx, http.MethodPost, "/v1/checkout/sessions/"+url.PathEscape(sessionID)+"/expire", url.Values{}, "", &CheckoutSession{})
}

func (p *StripeProvider) CreateRefund(ctx context.Context, input RefundInput) (*Refund, error) {
	form := url.Values{
		"payment_intent":          {input.PaymentIntentID},
		"metadata[purchase_id]":   {input.PurchaseID},
		"metadata[workspace_id]":  {input.WorkspaceID},
		"metadata[flipbook_id]":   {input.FlipbookID},
		"metadata[refund_reason]": {input.Reason},
	}
	var refund Refund
	if err := p.doJSON(ctx, http.MethodPost, "/v1/refunds", form, input.Idempotency, &refund); err != nil {
		return nil, err
	}
	return &refund, nil
}

func (p *StripeProvider) doJSON(ctx context.Context, method, path string, form url.Values, idempotency string, target any) error {
	var body io.Reader
	if form != nil {
		body = bytes.NewBufferString(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.baseURL.String(), "/")+path, body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProvider, err)
	}
	req.Header.Set("Authorization", "Bearer "+p.secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Flipbook-Sales/1.0")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProvider, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProvider, err)
	}
	if len(data) > maxProviderResponseBytes {
		return fmt.Errorf("%w: response too large", ErrProvider)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &payload)
		message := strings.TrimSpace(payload.Error.Message)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("%w: HTTP %d: %s", ErrProvider, response.StatusCode, message)
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: invalid JSON response", ErrProvider)
	}
	return nil
}

func validHostedCheckoutURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || loopbackHTTP(parsed)
}
