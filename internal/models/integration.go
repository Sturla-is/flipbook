package models

import "time"

type IntegrationStatus string

const (
	IntegrationPending      IntegrationStatus = "pending"
	IntegrationConnected    IntegrationStatus = "connected"
	IntegrationError        IntegrationStatus = "error"
	IntegrationDisconnected IntegrationStatus = "disconnected"
)

type IntegrationConnection struct {
	ID                    string            `bson:"_id" json:"id"`
	WorkspaceID           string            `bson:"workspace_id" json:"workspace_id"`
	UserID                string            `bson:"user_id" json:"user_id"`
	Provider              string            `bson:"provider" json:"provider"`
	ExternalAccountID     string            `bson:"external_account_id,omitempty" json:"external_account_id,omitempty"`
	DisplayName           string            `bson:"display_name,omitempty" json:"display_name,omitempty"`
	Status                IntegrationStatus `bson:"status" json:"status"`
	Scopes                []string          `bson:"scopes,omitempty" json:"scopes,omitempty"`
	AccessTokenEncrypted  string            `bson:"access_token_encrypted,omitempty" json:"-"`
	RefreshTokenEncrypted string            `bson:"refresh_token_encrypted,omitempty" json:"-"`
	TokenExpiresAt        *time.Time        `bson:"token_expires_at,omitempty" json:"token_expires_at,omitempty"`
	LastVerifiedAt        *time.Time        `bson:"last_verified_at,omitempty" json:"last_verified_at,omitempty"`
	LastError             string            `bson:"last_error,omitempty" json:"last_error,omitempty"`
	Metadata              map[string]string `bson:"metadata,omitempty" json:"metadata,omitempty"`
	CreatedAt             time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt             time.Time         `bson:"updated_at" json:"updated_at"`
	DisconnectedAt        *time.Time        `bson:"disconnected_at,omitempty" json:"disconnected_at,omitempty"`
}

type WebhookSubscription struct {
	ID               string     `bson:"_id" json:"id"`
	WorkspaceID      string     `bson:"workspace_id" json:"workspace_id"`
	URL              string     `bson:"url" json:"url"`
	SecretCiphertext string     `bson:"secret_ciphertext" json:"-"`
	Events           []string   `bson:"events" json:"events"`
	Enabled          bool       `bson:"enabled" json:"enabled"`
	CreatedAt        time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time  `bson:"updated_at" json:"updated_at"`
	DisabledAt       *time.Time `bson:"disabled_at,omitempty" json:"disabled_at,omitempty"`
}

type WebhookDeliveryStatus string

const (
	WebhookPending    WebhookDeliveryStatus = "pending"
	WebhookRetry      WebhookDeliveryStatus = "retry"
	WebhookDelivering WebhookDeliveryStatus = "delivering"
	WebhookDelivered  WebhookDeliveryStatus = "delivered"
	WebhookFailed     WebhookDeliveryStatus = "failed"
)

type WebhookDelivery struct {
	ID             string                `bson:"_id" json:"id"`
	WorkspaceID    string                `bson:"workspace_id" json:"workspace_id"`
	SubscriptionID string                `bson:"subscription_id" json:"subscription_id"`
	EventID        string                `bson:"event_id" json:"event_id"`
	EventType      string                `bson:"event_type" json:"event_type"`
	PayloadJSON    string                `bson:"payload_json" json:"payload_json"`
	Status         WebhookDeliveryStatus `bson:"status" json:"status"`
	Attempts       int                   `bson:"attempts" json:"attempts"`
	MaxAttempts    int                   `bson:"max_attempts" json:"max_attempts"`
	NextAttemptAt  time.Time             `bson:"next_attempt_at" json:"next_attempt_at"`
	LastAttemptAt  *time.Time            `bson:"last_attempt_at,omitempty" json:"last_attempt_at,omitempty"`
	LeaseOwner     string                `bson:"lease_owner,omitempty" json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time            `bson:"lease_expires_at,omitempty" json:"lease_expires_at,omitempty"`
	ResponseStatus int                   `bson:"response_status,omitempty" json:"response_status,omitempty"`
	LastError      string                `bson:"last_error,omitempty" json:"last_error,omitempty"`
	IdempotencyKey string                `bson:"idempotency_key,omitempty" json:"idempotency_key,omitempty"`
	CreatedAt      time.Time             `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time             `bson:"updated_at" json:"updated_at"`
	DeliveredAt    *time.Time            `bson:"delivered_at,omitempty" json:"delivered_at,omitempty"`
}
