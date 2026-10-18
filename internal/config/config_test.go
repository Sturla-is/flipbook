package config

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultsAndCapabilityGates(t *testing.T) {
	cfg := defaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("safe defaults are invalid: %v", err)
	}
	if cfg.Analytics.ConsentPolicy != ConsentRequired || cfg.Conversion.WorkerCount < 1 || cfg.Storage.Backend != "local" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.OIDC.Configured() || cfg.SMTP.Configured() || cfg.Stripe.Configured() ||
		cfg.Providers.Canva.Configured() || cfg.Storage.S3.Configured() {
		t.Fatal("external capability enabled without configuration")
	}
	cfg.CRM.Salesforce.Enabled = true
	cfg.CRM.Salesforce.AccessToken = "token"
	if cfg.CRM.SalesforceConfigured() {
		t.Fatal("Salesforce requires an instance URL")
	}
	cfg.CRM.Salesforce.InstanceURL = "https://example.my.salesforce.com"
	if !cfg.CRM.SalesforceConfigured() {
		t.Fatal("configured Salesforce provider was not detected")
	}
}

func TestYAMLAndEnvironmentLoading(t *testing.T) {
	yaml := `
base_url: "https://viewer.example.com"
security:
  https_mode: true
analytics:
  retention_days: 90
  consent_policy: anonymous
conversion:
  worker_count: 3
oidc:
  enabled: true
  issuer_url: "https://idp.example.com"
  client_id: "client"
  client_secret: "secret"
  redirect_url: "https://viewer.example.com/auth/callback"
`
	environment := map[string]string{
		"FLIPBOOK_CONFIG":                   "custom.yaml",
		"FLIPBOOK_ANALYTICS_RETENTION_DAYS": "45",
		"FLIPBOOK_SECURITY_TRUSTED_PROXIES": "127.0.0.1, 10.0.0.0/8",
		"FLIPBOOK_STORAGE_S3_BUCKET":        "renditions",
		"FLIPBOOK_STORAGE_S3_REGION":        "us-east-1",
		"FLIPBOOK_STORAGE_S3_ENABLED":       "true",
	}
	getenv := func(key string) string { return environment[key] }
	readFile := func(path string) ([]byte, error) {
		if path != "custom.yaml" {
			return nil, errors.New("not found")
		}
		return []byte(yaml), nil
	}
	cfg, err := load(getenv, readFile, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Analytics.RetentionDays != 45 || cfg.Analytics.ConsentPolicy != "anonymous" || cfg.Conversion.WorkerCount != 3 {
		t.Fatalf("YAML/environment overrides not applied: %+v", cfg)
	}
	if !cfg.Security.SecureCookies || !cfg.Security.HSTS || len(cfg.Security.TrustedProxies) != 2 {
		t.Fatalf("HTTPS/list settings not applied: %+v", cfg.Security)
	}
	if !cfg.OIDC.Configured() || !cfg.Storage.S3.Configured() {
		t.Fatal("fully configured capabilities were not detected")
	}
	if cfg.SessionSecret == "" || cfg.APIKey == "" || !cfg.SessionSecretGenerated || !cfg.APIKeyGenerated {
		t.Fatal("local ephemeral secret defaults were not generated")
	}
}

func TestStrictLoadingRejectsBadInput(t *testing.T) {
	getenv := func(key string) string {
		if key == "FLIPBOOK_CONVERSION_WORKER_COUNT" {
			return "not-a-number"
		}
		return ""
	}
	readFile := func(string) ([]byte, error) { return nil, errors.New("not found") }
	if _, err := load(getenv, readFile, true); err == nil || !strings.Contains(err.Error(), "FLIPBOOK_CONVERSION_WORKER_COUNT") {
		t.Fatalf("expected environment parse error, got %v", err)
	}
	cfg, err := load(getenv, readFile, false)
	if err != nil || cfg == nil || cfg.Conversion.WorkerCount != 2 {
		t.Fatalf("legacy best-effort load changed behavior: %+v, %v", cfg, err)
	}

	getenv = func(key string) string {
		if key == "FLIPBOOK_CONFIG" {
			return "bad.yaml"
		}
		return ""
	}
	readFile = func(string) ([]byte, error) { return []byte("analytics: [invalid"), nil }
	if _, err := load(getenv, readFile, true); err == nil {
		t.Fatal("expected YAML parse error")
	}
}

func TestPersistentSecretValidation(t *testing.T) {
	cfg, err := load(func(string) string { return "" }, func(string) ([]byte, error) { return nil, errors.New("not found") }, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Security.RequirePersistentSecrets = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("ephemeral secrets accepted when persistent secrets are required")
	}
	cfg.SessionSecretGenerated = false
	cfg.APIKeyGenerated = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("persistent secrets rejected: %v", err)
	}
}
