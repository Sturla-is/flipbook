package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/sales"
)

var (
	errSalesUnauthorized = errors.New("sales authentication required")
	errSalesForbidden    = errors.New("sales administrator role required")
)

func salesWorkspace(r *http.Request, required models.Role) (string, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.User == nil {
		return "", errSalesUnauthorized
	}
	if !principal.Role().AtLeast(required) {
		return "", errSalesForbidden
	}
	workspaceID := principal.WorkspaceID()
	if workspaceID == "" {
		workspaceID = models.DefaultWorkspaceID
	}
	return workspaceID, nil
}

func writeSalesAdminError(w http.ResponseWriter, err error, api bool) {
	status := http.StatusInternalServerError
	message := "the sales operation could not be completed"
	switch {
	case errors.Is(err, errSalesUnauthorized):
		status, message = http.StatusUnauthorized, "authentication required"
	case errors.Is(err, errSalesForbidden):
		status, message = http.StatusForbidden, "insufficient permissions"
	default:
		status, message = sales.HTTPStatus(err), sales.PublicMessage(err)
	}
	if api {
		salesJSONError(w, message, status)
		return
	}
	http.Error(w, message, status)
}

func salesWantsJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") ||
		strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

type salesTotalView struct {
	Currency string
	Orders   int64
	Gross    string
	Refunded string
	Net      string
}

type salesOrderView struct {
	ID          string
	Publication string
	Email       string
	Status      string
	Amount      string
	Currency    string
	CreatedAt   string
	CanRefund   bool
	CanRevoke   bool
}

type salesCustomerView struct {
	Email        string
	Currency     string
	Orders       int64
	Gross        string
	Refunded     string
	Net          string
	Publications int
	LastPurchase string
}

type adminSalesData struct {
	Totals     []salesTotalView
	Orders     []salesOrderView
	Customers  []salesCustomerView
	From       string
	To         string
	FlipbookID string
	Status     string
	Truncated  bool
	Configured bool
	CSRFToken  string
	Notice     string
}

func (h *SalesHandler) reportFromRequest(r *http.Request, limit int64) (string, *sales.Report, string, string, error) {
	workspaceID, err := salesWorkspace(r, models.RoleAdmin)
	if err != nil {
		return "", nil, "", "", err
	}
	fromValue := strings.TrimSpace(r.URL.Query().Get("from"))
	toValue := strings.TrimSpace(r.URL.Query().Get("to"))
	from, to, err := sales.DateRange(fromValue, toValue)
	if err != nil {
		return "", nil, "", "", err
	}
	report, err := h.service.Report(r.Context(), sales.ReportFilter{
		WorkspaceID: workspaceID, FlipbookID: strings.TrimSpace(r.URL.Query().Get("flipbook_id")),
		Status: models.PurchaseStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
		From:   from, To: to, Limit: limit,
	})
	return workspaceID, report, fromValue, toValue, err
}

func (h *SalesHandler) AdminSales(w http.ResponseWriter, r *http.Request) {
	_, report, from, to, err := h.reportFromRequest(r, 500)
	if err != nil {
		writeSalesAdminError(w, err, salesWantsJSON(r))
		return
	}
	if salesWantsJSON(r) {
		w.Header().Set("Cache-Control", "no-store")
		salesJSONResponse(w, report)
		return
	}
	data := adminSalesData{
		From: from, To: to, FlipbookID: r.URL.Query().Get("flipbook_id"), Status: r.URL.Query().Get("status"),
		Truncated: report.Truncated, Configured: h.service.Configured(), CSRFToken: auth.CSRFTokenFromContext(r.Context()),
		Notice: r.URL.Query().Get("notice"),
	}
	for _, total := range report.Totals {
		data.Totals = append(data.Totals, salesTotalView{
			Currency: total.Currency, Orders: total.Orders, Gross: sales.FormatAmount(total.GrossCents, total.Currency),
			Refunded: sales.FormatAmount(total.RefundedCents, total.Currency), Net: sales.FormatAmount(total.NetCents, total.Currency),
		})
	}
	for _, order := range report.Orders {
		purchase := order.Purchase
		data.Orders = append(data.Orders, salesOrderView{
			ID: purchase.ID, Publication: order.PublicationTitle, Email: purchase.Email, Status: string(purchase.Status),
			Amount: sales.FormatAmount(purchase.AmountCents, purchase.Currency), Currency: purchase.Currency,
			CreatedAt: purchase.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"),
			CanRefund: purchase.Status == models.PurchasePaid && h.service.Configured(),
			CanRevoke: purchase.Status == models.PurchasePaid,
		})
	}
	for _, customer := range report.Customers {
		data.Customers = append(data.Customers, salesCustomerView{
			Email: customer.Email, Currency: customer.Currency, Orders: customer.Orders,
			Gross: sales.FormatAmount(customer.GrossCents, customer.Currency), Refunded: sales.FormatAmount(customer.RefundedCents, customer.Currency),
			Net: sales.FormatAmount(customer.NetCents, customer.Currency), Publications: customer.PublicationCount,
			LastPurchase: customer.LastPurchasedAt.UTC().Format("2006-01-02"),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	h.renderSales(w, "admin_sales", data)
}

type adminSalesSettingsData struct {
	Flipbook   *models.Flipbook
	Price      string
	Configured bool
	Saved      bool
	CSRFToken  string
}

func (h *SalesHandler) AdminSalesSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := salesWorkspace(r, models.RolePublisher)
	if err != nil {
		writeSalesAdminError(w, err, false)
		return
	}
	fb, err := h.service.Publication(r.Context(), workspaceID, chi.URLParam(r, "id"))
	if err != nil {
		writeSalesAdminError(w, err, false)
		return
	}
	h.renderSales(w, "admin_sales_settings", adminSalesSettingsData{
		Flipbook: fb, Price: sales.FormatAmount(fb.Sales.PriceCents, fb.Sales.Currency),
		Configured: h.service.Configured(), Saved: r.URL.Query().Get("saved") == "1",
		CSRFToken: auth.CSRFTokenFromContext(r.Context()),
	})
}

func (h *SalesHandler) AdminUpdateSalesSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := salesWorkspace(r, models.RolePublisher)
	if err != nil {
		writeSalesAdminError(w, err, salesWantsJSON(r))
		return
	}
	settings, isForm, err := salesSettingsFromRequest(w, r)
	if err != nil {
		writeSalesAdminError(w, err, !isForm)
		return
	}
	fb, err := h.service.UpdateSettings(r.Context(), workspaceID, chi.URLParam(r, "id"), settings)
	if err != nil {
		writeSalesAdminError(w, err, !isForm)
		return
	}
	if isForm {
		http.Redirect(w, r, "/admin/flipbooks/"+fb.ID+"/sales?saved=1", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	salesJSONResponse(w, fb.Sales)
}

func salesSettingsFromRequest(w http.ResponseWriter, r *http.Request) (models.SalesSettings, bool, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var settings models.SalesSettings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			return settings, false, sales.ErrInvalid
		}
		return settings, false, nil
	}
	if err := r.ParseForm(); err != nil {
		return models.SalesSettings{}, true, sales.ErrInvalid
	}
	currency, err := sales.NormalizeCurrency(r.FormValue("currency"))
	if err != nil {
		return models.SalesSettings{}, true, err
	}
	price, err := sales.ParseAmount(r.FormValue("price"), currency)
	if err != nil {
		return models.SalesSettings{}, true, err
	}
	preview, err := strconv.Atoi(strings.TrimSpace(r.FormValue("preview_pages")))
	if err != nil {
		return models.SalesSettings{}, true, sales.ErrInvalid
	}
	return models.SalesSettings{
		Enabled:    r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true",
		PriceCents: price, Currency: currency, PreviewPages: preview,
		StripePriceID: strings.TrimSpace(r.FormValue("stripe_price_id")),
	}, true, nil
}

func (h *SalesHandler) AdminRefund(w http.ResponseWriter, r *http.Request) {
	h.adminMutation(w, r, true)
}

func (h *SalesHandler) AdminRevoke(w http.ResponseWriter, r *http.Request) {
	h.adminMutation(w, r, false)
}

func (h *SalesHandler) adminMutation(w http.ResponseWriter, r *http.Request, refund bool) {
	workspaceID, err := salesWorkspace(r, models.RoleAdmin)
	api := salesWantsJSON(r)
	if err != nil {
		writeSalesAdminError(w, err, api)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSalesAdminError(w, sales.ErrInvalid, api)
		return
	}
	purchaseID := chi.URLParam(r, "purchaseID")
	reason := strings.TrimSpace(r.FormValue("reason"))
	if refund {
		err = h.service.Refund(r.Context(), workspaceID, purchaseID, reason)
	} else {
		err = h.service.Revoke(r.Context(), workspaceID, purchaseID, reason)
	}
	if err != nil {
		writeSalesAdminError(w, err, api)
		return
	}
	notice := "access revoked"
	if refund {
		notice = "purchase refunded"
	}
	if api {
		salesJSONResponse(w, map[string]any{"ok": true, "message": notice})
		return
	}
	http.Redirect(w, r, "/admin/sales?notice="+url.QueryEscape(notice), http.StatusSeeOther)
}

func utcDay() string { return time.Now().UTC().Format("2006-01-02") }
