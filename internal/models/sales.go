package models

import "time"

type PurchaseStatus string

const (
	PurchasePending   PurchaseStatus = "pending"
	PurchasePaid      PurchaseStatus = "paid"
	PurchaseFailed    PurchaseStatus = "failed"
	PurchaseCancelled PurchaseStatus = "cancelled"
	PurchaseRefunded  PurchaseStatus = "refunded"
)

type Purchase struct {
	ID                      string            `bson:"_id" json:"id"`
	WorkspaceID             string            `bson:"workspace_id" json:"workspace_id"`
	FlipbookID              string            `bson:"flipbook_id" json:"flipbook_id"`
	UserID                  string            `bson:"user_id,omitempty" json:"user_id,omitempty"`
	Email                   string            `bson:"email,omitempty" json:"email,omitempty"`
	EmailHash               string            `bson:"email_hash,omitempty" json:"-"`
	AmountCents             int64             `bson:"amount_cents" json:"amount_cents"`
	Currency                string            `bson:"currency" json:"currency"`
	Status                  PurchaseStatus    `bson:"status" json:"status"`
	StripeCheckoutSessionID string            `bson:"stripe_checkout_session_id,omitempty" json:"stripe_checkout_session_id,omitempty"`
	StripePaymentIntentID   string            `bson:"stripe_payment_intent_id,omitempty" json:"stripe_payment_intent_id,omitempty"`
	StripeEventID           string            `bson:"stripe_event_id,omitempty" json:"stripe_event_id,omitempty"`
	Metadata                map[string]string `bson:"metadata,omitempty" json:"metadata,omitempty"`
	PurchasedAt             *time.Time        `bson:"purchased_at,omitempty" json:"purchased_at,omitempty"`
	RefundedAt              *time.Time        `bson:"refunded_at,omitempty" json:"refunded_at,omitempty"`
	CreatedAt               time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt               time.Time         `bson:"updated_at" json:"updated_at"`
}

type EntitlementStatus string

const (
	EntitlementActive  EntitlementStatus = "active"
	EntitlementRevoked EntitlementStatus = "revoked"
)

type Entitlement struct {
	ID               string            `bson:"_id" json:"id"`
	WorkspaceID      string            `bson:"workspace_id" json:"workspace_id"`
	FlipbookID       string            `bson:"flipbook_id" json:"flipbook_id"`
	PurchaseID       string            `bson:"purchase_id" json:"purchase_id"`
	UserID           string            `bson:"user_id,omitempty" json:"user_id,omitempty"`
	EmailHash        string            `bson:"email_hash,omitempty" json:"-"`
	Status           EntitlementStatus `bson:"status" json:"status"`
	GrantedAt        time.Time         `bson:"granted_at" json:"granted_at"`
	ExpiresAt        *time.Time        `bson:"expires_at,omitempty" json:"expires_at,omitempty"`
	RevokedAt        *time.Time        `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	RevocationReason string            `bson:"revocation_reason,omitempty" json:"revocation_reason,omitempty"`
}

func (e Entitlement) Active(now time.Time) bool {
	return e.Status == EntitlementActive && e.RevokedAt == nil &&
		(e.ExpiresAt == nil || now.Before(*e.ExpiresAt))
}

type SalesSummary struct {
	Orders        int64 `bson:"orders" json:"orders"`
	GrossCents    int64 `bson:"gross_cents" json:"gross_cents"`
	RefundedCents int64 `bson:"refunded_cents" json:"refunded_cents"`
	NetCents      int64 `bson:"net_cents" json:"net_cents"`
}
