package handlers

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
)

func apiAnalyticsFilter(r *http.Request, workspaceID string) (models.AnalyticsFilter, error) {
	filter := models.AnalyticsFilter{WorkspaceID: workspaceID}
	query := r.URL.Query()
	filter.FlipbookIDs = apiStringList(query.Get("flipbook_ids"))
	if len(filter.FlipbookIDs) == 0 && query.Get("flipbook_id") != "" {
		filter.FlipbookIDs = []string{query.Get("flipbook_id")}
	}
	for _, value := range apiStringList(query.Get("types")) {
		if value == "page_view" {
			value = string(models.AnalyticsPageExposure)
		}
		filter.Types = append(filter.Types, models.AnalyticsEventType(value))
	}
	var err error
	filter.From, err = apiTime(query.Get("from"))
	if err != nil {
		return filter, err
	}
	filter.To, err = apiTime(query.Get("to"))
	if err != nil {
		return filter, err
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		return filter, errors.New("from must be before to")
	}
	return filter, nil
}

func (h *APIHandler) AnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiAnalyticsFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	summary, err := h.db.AggregateAnalytics(r.Context(), filter)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, summary)
}

func (h *APIHandler) AnalyticsBreakdown(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiAnalyticsFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	dimension := r.URL.Query().Get("dimension")
	if dimension == "" {
		dimension = "day"
	}
	buckets, err := h.db.AnalyticsBreakdown(r.Context(), filter, dimension)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	jsonResponse(w, map[string]any{"dimension": dimension, "items": buckets})
}

func (h *APIHandler) AnalyticsPages(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiAnalyticsFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	stats, err := h.db.AnalyticsPageStats(r.Context(), filter)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"items": stats})
}

func (h *APIHandler) AnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiAnalyticsFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	limit, _, err := apiPage(r, 1000, 10000)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	events, err := h.db.ListAnalyticsEvents(r.Context(), filter, limit)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"items": events, "truncated": len(events) == int(limit)})
}

func (h *APIHandler) ExportAnalyticsCSV(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiAnalyticsFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	events, err := h.db.ListAnalyticsEvents(r.Context(), filter, 10000)
	if err != nil {
		apiError(w, err)
		return
	}
	records := make([][]string, 0, len(events)+1)
	records = append(records, []string{"id", "occurred_at", "flipbook_id", "type", "page", "active_seconds", "value", "referrer", "utm_source", "utm_medium", "utm_campaign", "placement", "device", "browser", "country", "search_terms", "target_host"})
	for _, event := range events {
		records = append(records, []string{
			event.ID, event.OccurredAt.Format(time.RFC3339Nano), event.FlipbookID, string(event.Type),
			strconv.Itoa(event.Page), strconv.FormatInt(event.ActiveSeconds, 10), strconv.FormatInt(event.Value, 10),
			event.Referrer, event.UTM.Source, event.UTM.Medium, event.UTM.Campaign, event.Placement,
			event.Device, event.Browser, event.Country, event.SearchTerms, event.TargetHost,
		})
	}
	writeAPICSV(w, "analytics-"+h.now().UTC().Format("2006-01-02")+".csv", records)
}

func apiDateRange(r *http.Request) (time.Time, time.Time, error) {
	from, err := apiTime(r.URL.Query().Get("from"))
	if err != nil {
		return from, time.Time{}, err
	}
	to, err := apiTime(r.URL.Query().Get("to"))
	return from, to, err
}

func (h *APIHandler) ListLeads(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	limit, offset, err := apiPage(r, 500, 10000)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	from, to, err := apiDateRange(r)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	flipbookID := r.URL.Query().Get("flipbook_id")
	leads, err := h.db.ListLeads(r.Context(), database.LeadFilter{
		WorkspaceID: identity.WorkspaceID, FlipbookID: flipbookID, From: from, To: to, Limit: limit, Offset: offset,
	})
	if err != nil {
		apiError(w, err)
		return
	}
	total, err := h.db.CountLeads(r.Context(), identity.WorkspaceID, flipbookID)
	if err != nil {
		apiError(w, err)
		return
	}
	if !from.IsZero() || !to.IsZero() {
		total = offset + int64(len(leads))
	}
	jsonResponse(w, map[string]any{
		"items":      leads,
		"pagination": apiPagination{Limit: limit, Offset: offset, Total: total, HasMore: len(leads) == int(limit)},
	})
}

func (h *APIHandler) DeleteLead(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	id := chi.URLParam(r, "leadID")
	if err := h.db.DeleteLead(r.Context(), identity.WorkspaceID, id); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "lead.delete", "lead", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandler) ExportLeadsCSV(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	from, to, err := apiDateRange(r)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	leads, err := h.db.ListLeads(r.Context(), database.LeadFilter{
		WorkspaceID: identity.WorkspaceID, FlipbookID: r.URL.Query().Get("flipbook_id"), From: from, To: to, Limit: 10000,
	})
	if err != nil {
		apiError(w, err)
		return
	}
	records := make([][]string, 0, len(leads)+1)
	records = append(records, []string{"id", "created_at", "flipbook_id", "email", "name", "consented", "consented_at", "source", "utm_source", "utm_medium", "utm_campaign", "metadata"})
	for _, lead := range leads {
		consentedAt := ""
		if lead.ConsentedAt != nil {
			consentedAt = lead.ConsentedAt.Format(time.RFC3339Nano)
		}
		metadata, _ := json.Marshal(lead.Metadata)
		records = append(records, []string{
			lead.ID, lead.CreatedAt.Format(time.RFC3339Nano), lead.FlipbookID, lead.Email, lead.Name,
			strconv.FormatBool(lead.Consented), consentedAt, lead.Source, lead.UTM.Source, lead.UTM.Medium, lead.UTM.Campaign, string(metadata),
		})
	}
	writeAPICSV(w, "leads-"+h.now().UTC().Format("2006-01-02")+".csv", records)
}

func apiPurchaseFilter(r *http.Request, workspaceID string) (database.PurchaseFilter, error) {
	from, to, err := apiDateRange(r)
	if err != nil {
		return database.PurchaseFilter{}, err
	}
	status := models.PurchaseStatus(r.URL.Query().Get("status"))
	if status != "" {
		switch status {
		case models.PurchasePending, models.PurchasePaid, models.PurchaseFailed, models.PurchaseCancelled, models.PurchaseRefunded:
		default:
			return database.PurchaseFilter{}, errors.New("unsupported purchase status")
		}
	}
	return database.PurchaseFilter{
		WorkspaceID: workspaceID, FlipbookID: r.URL.Query().Get("flipbook_id"), Status: status, From: from, To: to,
	}, nil
}

func (h *APIHandler) ListPurchases(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiPurchaseFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	filter.Limit, filter.Offset, err = apiPage(r, 500, 10000)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	purchases, err := h.db.ListPurchases(r.Context(), filter)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{
		"items":      purchases,
		"pagination": apiPagination{Limit: filter.Limit, Offset: filter.Offset, Total: filter.Offset + int64(len(purchases)), HasMore: len(purchases) == int(filter.Limit)},
	})
}

func (h *APIHandler) SalesSummary(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiPurchaseFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	summary, err := h.db.AggregateSales(r.Context(), filter)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, summary)
}

func (h *APIHandler) GetPurchase(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	purchase, err := h.db.GetPurchase(r.Context(), identity.WorkspaceID, chi.URLParam(r, "purchaseID"))
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, purchase)
}

func (h *APIHandler) ExportSalesCSV(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	filter, err := apiPurchaseFilter(r, identity.WorkspaceID)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	filter.Limit = 10000
	purchases, err := h.db.ListPurchases(r.Context(), filter)
	if err != nil {
		apiError(w, err)
		return
	}
	records := make([][]string, 0, len(purchases)+1)
	records = append(records, []string{"id", "created_at", "flipbook_id", "email", "amount_cents", "currency", "status", "purchased_at", "refunded_at", "stripe_checkout_session_id"})
	for _, purchase := range purchases {
		purchasedAt, refundedAt := "", ""
		if purchase.PurchasedAt != nil {
			purchasedAt = purchase.PurchasedAt.Format(time.RFC3339Nano)
		}
		if purchase.RefundedAt != nil {
			refundedAt = purchase.RefundedAt.Format(time.RFC3339Nano)
		}
		records = append(records, []string{
			purchase.ID, purchase.CreatedAt.Format(time.RFC3339Nano), purchase.FlipbookID, purchase.Email,
			strconv.FormatInt(purchase.AmountCents, 10), purchase.Currency, string(purchase.Status), purchasedAt, refundedAt, purchase.StripeCheckoutSessionID,
		})
	}
	writeAPICSV(w, "sales-"+h.now().UTC().Format("2006-01-02")+".csv", records)
}

type entitlementRequest struct {
	Reason string `json:"reason"`
}

func (h *APIHandler) RevokePurchaseEntitlement(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleAdmin)
	if err != nil {
		apiError(w, err)
		return
	}
	var request entitlementRequest
	if r.Body != nil && r.ContentLength != 0 {
		_ = decodeAPIJSON(r, &request)
	}
	purchase, err := h.db.GetPurchase(r.Context(), identity.WorkspaceID, chi.URLParam(r, "purchaseID"))
	if err != nil {
		apiError(w, err)
		return
	}
	entitlement, err := h.db.GetEntitlementByPurchase(r.Context(), purchase.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	if entitlement.WorkspaceID != identity.WorkspaceID {
		invalidAPIRequest(w, "entitlement does not belong to this workspace")
		return
	}
	if err := h.db.RevokeEntitlement(r.Context(), identity.WorkspaceID, entitlement.ID, strings.TrimSpace(request.Reason)); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "sales.entitlement.revoke", "purchase", purchase.ID, nil)
	jsonResponse(w, map[string]any{"purchase_id": purchase.ID, "entitlement_id": entitlement.ID, "status": models.EntitlementRevoked})
}

func (h *APIHandler) RestorePurchaseEntitlement(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleAdmin)
	if err != nil {
		apiError(w, err)
		return
	}
	purchase, err := h.db.GetPurchase(r.Context(), identity.WorkspaceID, chi.URLParam(r, "purchaseID"))
	if err != nil {
		apiError(w, err)
		return
	}
	if purchase.Status != models.PurchasePaid {
		invalidAPIRequest(w, "only a paid, non-refunded purchase can be restored")
		return
	}
	entitlement, err := h.db.GetEntitlementByPurchase(r.Context(), purchase.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	if err := h.db.GrantEntitlement(r.Context(), entitlement); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "sales.entitlement.restore", "purchase", purchase.ID, nil)
	jsonResponse(w, map[string]any{"purchase_id": purchase.ID, "entitlement_id": entitlement.ID, "status": models.EntitlementActive})
}

func (h *APIHandler) ListSubscribers(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	subscribers, err := h.db.ListSubscribers(r.Context(), identity.WorkspaceID, models.SubscriberStatus(r.URL.Query().Get("status")))
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"items": subscribers})
}

func (h *APIHandler) DeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	if err := h.db.DeleteSubscriber(r.Context(), identity.WorkspaceID, chi.URLParam(r, "subscriberID")); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAPICSV(w http.ResponseWriter, filename string, records [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writer := csv.NewWriter(w)
	_ = writer.WriteAll(records)
	writer.Flush()
}
