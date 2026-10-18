package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (d *DB) CreatePurchase(ctx context.Context, purchase *models.Purchase) error {
	if purchase.WorkspaceID == "" || purchase.FlipbookID == "" || purchase.AmountCents < 0 {
		return errors.New("purchase workspace, flipbook, and non-negative amount are required")
	}
	if purchase.UserID == "" && purchase.EmailHash == "" {
		return errors.New("purchase user or email hash is required")
	}
	purchase.Currency = strings.ToUpper(purchase.Currency)
	if len(purchase.Currency) != 3 {
		return errors.New("purchase currency must be a three-letter code")
	}
	if purchase.ID == "" {
		purchase.ID = newID()
	}
	if purchase.Status == "" {
		purchase.Status = models.PurchasePending
	}
	now := time.Now().UTC()
	if purchase.CreatedAt.IsZero() {
		purchase.CreatedAt = now
	}
	purchase.UpdatedAt = now
	_, err := d.purchases.InsertOne(ctx, purchase)
	return err
}

func (d *DB) GetPurchase(ctx context.Context, workspaceID, purchaseID string) (*models.Purchase, error) {
	var purchase models.Purchase
	filter := bson.M{"_id": purchaseID}
	if workspaceID != "" {
		filter["workspace_id"] = workspaceID
	}
	if err := d.purchases.FindOne(ctx, filter).Decode(&purchase); err != nil {
		return nil, err
	}
	return &purchase, nil
}

func (d *DB) GetPurchaseByCheckoutSession(ctx context.Context, sessionID string) (*models.Purchase, error) {
	var purchase models.Purchase
	if err := d.purchases.FindOne(ctx, bson.M{"stripe_checkout_session_id": sessionID}).Decode(&purchase); err != nil {
		return nil, err
	}
	return &purchase, nil
}

func (d *DB) MarkPurchasePaid(ctx context.Context, purchaseID, stripeEventID string, entitlement *models.Entitlement) error {
	purchase, err := d.GetPurchase(ctx, "", purchaseID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if purchase.Status != models.PurchasePaid {
		if purchase.Status != models.PurchasePending {
			return ErrConflict
		}
		set := bson.M{"status": models.PurchasePaid, "purchased_at": now, "updated_at": now}
		if stripeEventID != "" {
			set["stripe_event_id"] = stripeEventID
		}
		result, err := d.purchases.UpdateOne(ctx, bson.M{"_id": purchaseID, "status": models.PurchasePending}, bson.M{"$set": set})
		if err != nil {
			return err
		}
		if result.MatchedCount == 0 {
			return ErrConflict
		}
	}
	if entitlement == nil {
		return errors.New("paid purchase entitlement is required")
	}
	entitlement.WorkspaceID = purchase.WorkspaceID
	entitlement.FlipbookID = purchase.FlipbookID
	entitlement.PurchaseID = purchase.ID
	if entitlement.UserID == "" {
		entitlement.UserID = purchase.UserID
	}
	if entitlement.EmailHash == "" {
		entitlement.EmailHash = purchase.EmailHash
	}
	return d.EnsureEntitlement(ctx, entitlement)
}

func (d *DB) RefundPurchase(ctx context.Context, workspaceID, purchaseID, stripeEventID, reason string) error {
	now := time.Now().UTC()
	set := bson.M{"status": models.PurchaseRefunded, "refunded_at": now, "updated_at": now}
	if stripeEventID != "" {
		set["stripe_event_id"] = stripeEventID
	}
	result, err := d.purchases.UpdateOne(ctx, bson.M{
		"_id": purchaseID, "workspace_id": workspaceID, "status": bson.M{"$in": bson.A{models.PurchasePaid, models.PurchaseRefunded}},
	}, bson.M{"$set": set})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrConflict
	}
	_, err = d.entitlements.UpdateMany(ctx, bson.M{"purchase_id": purchaseID, "workspace_id": workspaceID}, bson.M{"$set": bson.M{
		"status": models.EntitlementRevoked, "revoked_at": now, "revocation_reason": reason,
	}})
	return err
}

type PurchaseFilter struct {
	WorkspaceID string
	FlipbookID  string
	Status      models.PurchaseStatus
	From        time.Time
	To          time.Time
	Limit       int64
	Offset      int64
}

func purchaseQuery(filter PurchaseFilter) bson.M {
	query := bson.M{"workspace_id": filter.WorkspaceID}
	if filter.FlipbookID != "" {
		query["flipbook_id"] = filter.FlipbookID
	}
	if filter.Status != "" {
		query["status"] = filter.Status
	}
	if !filter.From.IsZero() || !filter.To.IsZero() {
		rangeQuery := bson.M{}
		if !filter.From.IsZero() {
			rangeQuery["$gte"] = filter.From
		}
		if !filter.To.IsZero() {
			rangeQuery["$lt"] = filter.To
		}
		query["created_at"] = rangeQuery
	}
	return query
}

func (d *DB) ListPurchases(ctx context.Context, filter PurchaseFilter) ([]*models.Purchase, error) {
	if filter.WorkspaceID == "" {
		return nil, errors.New("purchase workspace is required")
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 1000
	}
	if limit < 1 || limit > 10000 || filter.Offset < 0 {
		return nil, errors.New("invalid purchase pagination")
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit).SetSkip(filter.Offset)
	return decodeCursor[models.Purchase](ctx, d.purchases, purchaseQuery(filter), opts)
}

func (d *DB) AggregateSales(ctx context.Context, filter PurchaseFilter) (models.SalesSummary, error) {
	var summary models.SalesSummary
	if filter.WorkspaceID == "" {
		return summary, errors.New("purchase workspace is required")
	}
	query := purchaseQuery(filter)
	query["status"] = bson.M{"$in": bson.A{models.PurchasePaid, models.PurchaseRefunded}}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: query}},
		{{Key: "$group", Value: bson.M{
			"_id": nil, "orders": bson.M{"$sum": 1}, "gross_cents": bson.M{"$sum": "$amount_cents"},
			"refunded_cents": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$status", models.PurchaseRefunded}}, "$amount_cents", 0}}},
		}}},
	}
	cursor, err := d.purchases.Aggregate(ctx, pipeline)
	if err != nil {
		return summary, err
	}
	defer cursor.Close(ctx)
	if cursor.Next(ctx) {
		if err := cursor.Decode(&summary); err != nil {
			return summary, err
		}
		summary.NetCents = summary.GrossCents - summary.RefundedCents
	}
	return summary, cursor.Err()
}

func prepareEntitlement(entitlement *models.Entitlement) error {
	if entitlement.WorkspaceID == "" || entitlement.FlipbookID == "" || entitlement.PurchaseID == "" {
		return errors.New("entitlement workspace, flipbook, and purchase are required")
	}
	if entitlement.UserID == "" && entitlement.EmailHash == "" {
		return errors.New("entitlement user or email hash is required")
	}
	if entitlement.ID == "" {
		entitlement.ID = newID()
	}
	if entitlement.GrantedAt.IsZero() {
		entitlement.GrantedAt = time.Now().UTC()
	}
	entitlement.Status = models.EntitlementActive
	entitlement.RevokedAt = nil
	entitlement.RevocationReason = ""
	return nil
}

// EnsureEntitlement is idempotent and never reactivates an entitlement that was
// explicitly revoked. It is suitable for replayed payment webhooks.
func (d *DB) EnsureEntitlement(ctx context.Context, entitlement *models.Entitlement) error {
	if err := prepareEntitlement(entitlement); err != nil {
		return err
	}
	_, err := d.entitlements.UpdateOne(ctx, bson.M{"purchase_id": entitlement.PurchaseID},
		bson.M{"$setOnInsert": entitlement}, options.UpdateOne().SetUpsert(true))
	return err
}

// GrantEntitlement explicitly grants or reactivates access.
func (d *DB) GrantEntitlement(ctx context.Context, entitlement *models.Entitlement) error {
	if err := prepareEntitlement(entitlement); err != nil {
		return err
	}
	set := bson.M{
		"workspace_id": entitlement.WorkspaceID, "flipbook_id": entitlement.FlipbookID,
		"purchase_id": entitlement.PurchaseID, "user_id": entitlement.UserID,
		"email_hash": entitlement.EmailHash, "status": entitlement.Status,
		"granted_at": entitlement.GrantedAt, "expires_at": entitlement.ExpiresAt,
		"revoked_at": nil, "revocation_reason": "",
	}
	_, err := d.entitlements.UpdateOne(ctx, bson.M{"purchase_id": entitlement.PurchaseID}, bson.M{
		"$set": set, "$setOnInsert": bson.M{"_id": entitlement.ID},
	}, options.UpdateOne().SetUpsert(true))
	return err
}

func (d *DB) GetEntitlementByPurchase(ctx context.Context, purchaseID string) (*models.Entitlement, error) {
	var entitlement models.Entitlement
	if err := d.entitlements.FindOne(ctx, bson.M{"purchase_id": purchaseID}).Decode(&entitlement); err != nil {
		return nil, err
	}
	return &entitlement, nil
}

func (d *DB) HasEntitlement(ctx context.Context, workspaceID, flipbookID, userID, emailHash string, now time.Time) (bool, error) {
	if userID == "" && emailHash == "" {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	identities := bson.A{}
	if userID != "" {
		identities = append(identities, bson.M{"user_id": userID})
	}
	if emailHash != "" {
		identities = append(identities, bson.M{"email_hash": emailHash})
	}
	query := bson.M{
		"workspace_id": workspaceID, "flipbook_id": flipbookID,
		"status": models.EntitlementActive, "revoked_at": nil,
		"$and": bson.A{
			bson.M{"$or": identities},
			bson.M{"$or": bson.A{bson.M{"expires_at": nil}, bson.M{"expires_at": bson.M{"$gt": now}}}},
		},
	}
	count, err := d.entitlements.CountDocuments(ctx, query, options.Count().SetLimit(1))
	return count > 0, err
}

func (d *DB) RevokeEntitlement(ctx context.Context, workspaceID, entitlementID, reason string) error {
	now := time.Now().UTC()
	result, err := d.entitlements.UpdateOne(ctx, bson.M{"_id": entitlementID, "workspace_id": workspaceID}, bson.M{"$set": bson.M{
		"status": models.EntitlementRevoked, "revoked_at": now, "revocation_reason": reason,
	}})
	if err == nil && result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return err
}
