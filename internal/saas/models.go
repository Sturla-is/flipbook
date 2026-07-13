package saas

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// These models mirror the LastSaaS MongoDB document shapes.
// They are read-only — flipbook never writes to these collections.

// Tenant mirrors the LastSaaS tenants collection.
type Tenant struct {
	ID                   bson.ObjectID  `bson:"_id"`
	Name                 string         `bson:"name"`
	Slug                 string         `bson:"slug"`
	IsRoot               bool           `bson:"isRoot"`
	IsActive             bool           `bson:"isActive"`
	PlanID               *bson.ObjectID `bson:"planId,omitempty"`
	BillingWaived        bool           `bson:"billingWaived"`
	SubscriptionCredits  int64          `bson:"subscriptionCredits"`
	PurchasedCredits     int64          `bson:"purchasedCredits"`
	StripeCustomerID     string         `bson:"stripeCustomerId,omitempty"`
	BillingStatus        string         `bson:"billingStatus"`
	StripeSubscriptionID string         `bson:"stripeSubscriptionId,omitempty"`
	BillingInterval      string         `bson:"billingInterval,omitempty"`
	CurrentPeriodEnd     *time.Time     `bson:"currentPeriodEnd,omitempty"`
	CanceledAt           *time.Time     `bson:"canceledAt,omitempty"`
	CreatedAt            time.Time      `bson:"createdAt"`
	UpdatedAt            time.Time      `bson:"updatedAt"`
}

// TenantMembership mirrors the LastSaaS tenant_memberships collection.
type TenantMembership struct {
	ID       bson.ObjectID `bson:"_id"`
	UserID   bson.ObjectID `bson:"userId"`
	TenantID bson.ObjectID `bson:"tenantId"`
	Role     string        `bson:"role"`
	JoinedAt time.Time     `bson:"joinedAt"`
}

// EntitlementValue mirrors the LastSaaS plan entitlement value.
type EntitlementValue struct {
	Type         string `bson:"type"`         // "bool" or "numeric"
	BoolValue    bool   `bson:"boolValue"`
	NumericValue int64  `bson:"numericValue"`
	Description  string `bson:"description"`
}

// Plan mirrors the LastSaaS plans collection.
type Plan struct {
	ID                   bson.ObjectID               `bson:"_id"`
	Name                 string                      `bson:"name"`
	Description          string                      `bson:"description"`
	MonthlyPriceCents    int64                       `bson:"monthlyPriceCents"`
	AnnualDiscountPct    int                         `bson:"annualDiscountPct"`
	UsageCreditsPerMonth int64                       `bson:"usageCreditsPerMonth"`
	CreditResetPolicy    string                      `bson:"creditResetPolicy"`
	BonusCredits         int64                       `bson:"bonusCredits"`
	UserLimit            int                         `bson:"userLimit"`
	Entitlements         map[string]EntitlementValue `bson:"entitlements"`
	IsSystem             bool                        `bson:"isSystem"`
	IsArchived           bool                        `bson:"isArchived"`
	CreatedAt            time.Time                   `bson:"createdAt"`
	UpdatedAt            time.Time                   `bson:"updatedAt"`
}
