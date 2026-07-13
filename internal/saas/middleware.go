package saas

import (
	"context"
	"net/http"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type contextKey string

const (
	ctxUserID   contextKey = "saas_user_id"
	ctxEmail    contextKey = "saas_email"
	ctxTenantID contextKey = "saas_tenant_id"
	ctxTenant   contextKey = "saas_tenant"
	ctxPlan     contextKey = "saas_plan"
)

// Middleware provides JWT auth and tenant resolution for SaaS routes.
type Middleware struct {
	jwt     *JWTValidator
	tenants *mongo.Collection
	members *mongo.Collection
	plans   *mongo.Collection
}

func NewMiddleware(jwt *JWTValidator, db *mongo.Database) *Middleware {
	return &Middleware{
		jwt:     jwt,
		tenants: db.Collection("tenants"),
		members: db.Collection("tenant_memberships"),
		plans:   db.Collection("plans"),
	}
}

// RequireJWT validates the Authorization header and puts user info in context.
func (m *Middleware) RequireJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		claims, err := m.jwt.Validate(token)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxUserID, claims.UserID)
		ctx = context.WithValue(ctx, ctxEmail, claims.Email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireTenant reads X-Tenant-ID, verifies membership, and loads tenant + plan.
func (m *Middleware) RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantIDStr := r.Header.Get("X-Tenant-ID")
		if tenantIDStr == "" {
			http.Error(w, `{"error":"missing X-Tenant-ID header"}`, http.StatusBadRequest)
			return
		}

		tenantOID, err := bson.ObjectIDFromHex(tenantIDStr)
		if err != nil {
			http.Error(w, `{"error":"invalid tenant ID"}`, http.StatusBadRequest)
			return
		}

		userIDStr := UserIDFromContext(r.Context())
		if userIDStr == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		userOID, err := bson.ObjectIDFromHex(userIDStr)
		if err != nil {
			http.Error(w, `{"error":"invalid user ID"}`, http.StatusBadRequest)
			return
		}

		// Verify user is a member of this tenant
		var membership TenantMembership
		err = m.members.FindOne(r.Context(), bson.M{
			"userId":   userOID,
			"tenantId": tenantOID,
		}).Decode(&membership)
		if err != nil {
			http.Error(w, `{"error":"not a member of this tenant"}`, http.StatusForbidden)
			return
		}

		// Load tenant
		var tenant Tenant
		err = m.tenants.FindOne(r.Context(), bson.M{"_id": tenantOID}).Decode(&tenant)
		if err != nil {
			http.Error(w, `{"error":"tenant not found"}`, http.StatusNotFound)
			return
		}

		if !tenant.IsActive {
			http.Error(w, `{"error":"tenant is deactivated"}`, http.StatusForbidden)
			return
		}

		// Load plan if assigned
		var plan *Plan
		if tenant.PlanID != nil {
			var p Plan
			if err := m.plans.FindOne(r.Context(), bson.M{"_id": *tenant.PlanID}).Decode(&p); err == nil {
				plan = &p
			}
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxTenantID, tenantIDStr)
		ctx = context.WithValue(ctx, ctxTenant, &tenant)
		ctx = context.WithValue(ctx, ctxPlan, plan)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Context helpers

func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

func TenantIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxTenantID).(string)
	return v
}

func TenantFromContext(ctx context.Context) *Tenant {
	v, _ := ctx.Value(ctxTenant).(*Tenant)
	return v
}

func PlanFromContext(ctx context.Context) *Plan {
	v, _ := ctx.Value(ctxPlan).(*Plan)
	return v
}
