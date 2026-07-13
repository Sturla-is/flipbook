package saas

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// BillingChecker provides helpers for checking tenant billing status
// and plan entitlements. It reads directly from shared MongoDB collections.
type BillingChecker struct {
	tenants   *mongo.Collection
	plans     *mongo.Collection
	flipbooks *mongo.Collection
	graceDays int
}

func NewBillingChecker(db *mongo.Database, graceDays int) *BillingChecker {
	return &BillingChecker{
		tenants:   db.Collection("tenants"),
		plans:     db.Collection("plans"),
		flipbooks: db.Collection("flipbooks"),
		graceDays: graceDays,
	}
}

// TenantCanServe checks if a tenant's flipbooks should be served publicly.
// Returns true if the tenant is active with valid billing (or within grace period).
func (bc *BillingChecker) TenantCanServe(ctx context.Context, tenantID string) bool {
	tenantOID, err := bson.ObjectIDFromHex(tenantID)
	if err != nil {
		return false
	}

	var tenant Tenant
	if err := bc.tenants.FindOne(ctx, bson.M{"_id": tenantOID}).Decode(&tenant); err != nil {
		return false
	}

	return bc.tenantStatusOK(&tenant)
}

// tenantStatusOK checks whether the tenant should have content served.
func (bc *BillingChecker) tenantStatusOK(tenant *Tenant) bool {
	if !tenant.IsActive {
		return false
	}

	if tenant.BillingWaived || tenant.IsRoot {
		return true
	}

	switch tenant.BillingStatus {
	case "active", "none":
		// "none" means they haven't set up billing yet (on free plan) — still serve
		return true
	case "past_due":
		// Grace period: serve for N days after payment failure
		if tenant.CurrentPeriodEnd != nil {
			deadline := tenant.CurrentPeriodEnd.Add(time.Duration(bc.graceDays) * 24 * time.Hour)
			return time.Now().Before(deadline)
		}
		return false
	case "canceled":
		// Canceled: serve until current period ends
		if tenant.CurrentPeriodEnd != nil {
			return time.Now().Before(*tenant.CurrentPeriodEnd)
		}
		return false
	default:
		return false
	}
}

// GetPageLimit returns the maxPages entitlement for a plan. Returns 0 if
// no plan is assigned or the entitlement doesn't exist.
func GetPageLimit(plan *Plan) int64 {
	if plan == nil {
		return 0
	}
	ent, ok := plan.Entitlements["maxPages"]
	if !ok || ent.Type != "numeric" {
		return 0
	}
	return ent.NumericValue
}

// CountTenantPages sums the page_count of all active (non-deleted) flipbooks
// owned by a tenant.
func (bc *BillingChecker) CountTenantPages(ctx context.Context, tenantID string) (int64, error) {
	pipeline := bson.A{
		bson.M{"$match": bson.M{
			"tenant_id":  tenantID,
			"deleted_at": nil,
		}},
		bson.M{"$group": bson.M{
			"_id":   nil,
			"total": bson.M{"$sum": "$page_count"},
		}},
	}

	cursor, err := bc.flipbooks.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var result struct {
		Total int64 `bson:"total"`
	}
	if cursor.Next(ctx) {
		if err := cursor.Decode(&result); err != nil {
			return 0, err
		}
	}
	return result.Total, nil
}

// CanUpload checks whether the tenant is allowed to upload more flipbooks
// based on their plan's page limit and current usage.
func (bc *BillingChecker) CanUpload(ctx context.Context, tenant *Tenant, plan *Plan) (bool, int64, int64) {
	limit := GetPageLimit(plan)
	if limit <= 0 {
		return false, 0, limit
	}

	used, err := bc.CountTenantPages(ctx, tenant.ID.Hex())
	if err != nil {
		return false, 0, limit
	}

	return used < limit, used, limit
}

// LookupTenantForFlipbook loads the tenant that owns a flipbook (by tenant_id field).
// Returns nil if the flipbook has no tenant (legacy admin flipbook).
func (bc *BillingChecker) LookupTenantForFlipbook(ctx context.Context, tenantID string) *Tenant {
	if tenantID == "" {
		return nil
	}
	tenantOID, err := bson.ObjectIDFromHex(tenantID)
	if err != nil {
		return nil
	}
	var tenant Tenant
	if err := bc.tenants.FindOne(ctx, bson.M{"_id": tenantOID}).Decode(&tenant); err != nil {
		return nil
	}
	return &tenant
}
