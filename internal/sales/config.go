package sales

import (
	"net/http"

	"github.com/jonradoff/flipbook/internal/config"
)

func NewWithConfig(store Store, cfg *config.Config) (*Service, error) {
	return NewWithConfigAndClient(store, cfg, nil)
}

func NewWithConfigAndClient(store Store, cfg *config.Config, client HTTPClient) (*Service, error) {
	if cfg == nil {
		return nil, ErrNotConfigured
	}
	return New(store, Options{
		StripeEnabled:  cfg.Stripe.Configured(),
		SecretKey:      cfg.Stripe.SecretKey,
		WebhookSecret:  cfg.Stripe.WebhookSecret,
		SigningSecret:  cfg.SessionSecret,
		BaseURL:        cfg.BaseURL,
		SuccessURL:     cfg.Stripe.SuccessURL,
		CancelURL:      cfg.Stripe.CancelURL,
		HTTPClient:     client,
		CookieSecure:   cfg.Security.SecureCookies || cfg.Security.HTTPSMode,
		CookieSameSite: http.SameSiteLaxMode,
	})
}
