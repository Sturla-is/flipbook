package handlers

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/sales"
)

func (h *SalesHandler) AdminSalesCSV(w http.ResponseWriter, r *http.Request) {
	_, report, _, _, err := h.reportFromRequest(r, 10000)
	if err != nil {
		writeSalesAdminError(w, err, false)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sales-`+utcDay()+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{
		"order_id", "created_at", "purchased_at", "status", "workspace_id", "flipbook_id",
		"publication", "customer_email", "amount", "currency", "amount_minor_units",
		"refunded_at", "stripe_checkout_session_id", "stripe_payment_intent_id",
	})
	for _, order := range report.Orders {
		purchase := order.Purchase
		_ = writer.Write([]string{
			csvSafe(purchase.ID), purchase.CreatedAt.UTC().Format(time.RFC3339), csvTime(purchase.PurchasedAt),
			string(purchase.Status), csvSafe(purchase.WorkspaceID), csvSafe(purchase.FlipbookID), csvSafe(order.PublicationTitle),
			csvSafe(purchase.Email), sales.FormatAmount(purchase.AmountCents, purchase.Currency), purchase.Currency,
			strconv.FormatInt(purchase.AmountCents, 10), csvTime(purchase.RefundedAt),
			csvSafe(purchase.StripeCheckoutSessionID), csvSafe(purchase.StripePaymentIntentID),
		})
	}
	writer.Flush()
}

func (h *SalesHandler) AdminCustomersCSV(w http.ResponseWriter, r *http.Request) {
	_, report, _, _, err := h.reportFromRequest(r, 10000)
	if err != nil {
		writeSalesAdminError(w, err, false)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sales-customers-`+utcDay()+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{
		"customer_email", "currency", "orders", "gross", "refunded", "net",
		"gross_minor_units", "refunded_minor_units", "net_minor_units",
		"publication_count", "first_purchase_at", "last_purchase_at",
	})
	for _, customer := range report.Customers {
		_ = writer.Write([]string{
			csvSafe(customer.Email), customer.Currency, strconv.FormatInt(customer.Orders, 10),
			sales.FormatAmount(customer.GrossCents, customer.Currency), sales.FormatAmount(customer.RefundedCents, customer.Currency),
			sales.FormatAmount(customer.NetCents, customer.Currency), strconv.FormatInt(customer.GrossCents, 10),
			strconv.FormatInt(customer.RefundedCents, 10), strconv.FormatInt(customer.NetCents, 10),
			strconv.Itoa(customer.PublicationCount), customer.FirstPurchasedAt.UTC().Format(time.RFC3339),
			customer.LastPurchasedAt.UTC().Format(time.RFC3339),
		})
	}
	writer.Flush()
}

func csvTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") ||
		strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@") {
		return "'" + value
	}
	return value
}
