package handlers

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
	"github.com/jonradoff/flipbook/internal/sales"
)

type SalesHandler struct {
	service *sales.Service
	tmpl    *template.Template
	baseURL string
}

func NewSalesHandler(service *sales.Service, tmpl *template.Template, baseURL string) *SalesHandler {
	return &SalesHandler{service: service, tmpl: tmpl, baseURL: strings.TrimRight(baseURL, "/")}
}

func NewSalesHandlerWithConfig(store sales.Store, cfg *config.Config, tmpl *template.Template) (*SalesHandler, error) {
	service, err := sales.NewWithConfig(store, cfg)
	if err != nil {
		return nil, err
	}
	return NewSalesHandler(service, tmpl, cfg.BaseURL), nil
}

func (h *SalesHandler) PurchaserReader(r *http.Request, workspaceID, flipbookID string) (publishing.Reader, error) {
	return h.service.ReaderFromRequest(r.Context(), r, workspaceID, flipbookID)
}

type salesCheckoutPageData struct {
	Quote     *sales.Quote
	Endpoint  string
	Email     string
	CSRFToken string
	BaseURL   string
}

func (h *SalesHandler) CheckoutPage(w http.ResponseWriter, r *http.Request) {
	quote, err := h.service.Quote(r.Context(), models.DefaultWorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		h.renderSalesError(w, r, err)
		return
	}
	email := ""
	if user, ok := auth.UserFromContext(r.Context()); ok {
		email = user.Email
	}
	h.renderSales(w, "sales_checkout", salesCheckoutPageData{
		Quote: quote, Endpoint: "/api/public/flipbooks/" + quote.Flipbook.ID + "/checkout",
		Email: email, CSRFToken: auth.CSRFTokenFromContext(r.Context()), BaseURL: h.baseURL,
	})
}

func (h *SalesHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	email, isForm, err := purchaserEmailFromRequest(r)
	if err != nil {
		salesJSONError(w, sales.PublicMessage(err), sales.HTTPStatus(err))
		return
	}
	if email == "" {
		if user, ok := auth.UserFromContext(r.Context()); ok {
			email = user.Email
		}
	}
	result, err := h.service.Checkout(r.Context(), sales.CheckoutInput{
		WorkspaceID: models.DefaultWorkspaceID, FlipbookID: chi.URLParam(r, "id"), Email: email,
	})
	if err != nil {
		salesJSONError(w, sales.PublicMessage(err), sales.HTTPStatus(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if isForm {
		http.Redirect(w, r, result.CheckoutURL, http.StatusSeeOther)
		return
	}
	salesJSONResponse(w, map[string]string{"checkout_url": result.CheckoutURL})
}

func purchaserEmailFromRequest(r *http.Request) (string, bool, error) {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		var input struct {
			Email string `json:"email"`
		}
		err := json.NewDecoder(r.Body).Decode(&input)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		if err != nil {
			return "", false, sales.ErrInvalid
		}
		return input.Email, false, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", true, sales.ErrInvalid
	}
	return r.FormValue("email"), true, nil
}

func (h *SalesHandler) CheckoutSuccess(w http.ResponseWriter, r *http.Request) {
	purchase, err := h.service.CompleteCheckout(r.Context(), r.URL.Query().Get("session_id"))
	if err != nil {
		h.renderSalesError(w, r, err)
		return
	}
	cookie, err := h.service.CookieForPurchase(r.Context(), purchase.WorkspaceID, purchase.ID)
	if err != nil {
		h.renderSalesError(w, r, err)
		return
	}
	http.SetCookie(w, cookie)
	fb, err := h.service.Publication(r.Context(), purchase.WorkspaceID, purchase.FlipbookID)
	if err != nil {
		h.renderSalesError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.renderSales(w, "sales_success", map[string]any{
		"Purchase": purchase, "Flipbook": fb, "ViewerURL": "/v/" + fb.Slug,
	})
}

func (h *SalesHandler) CheckoutCancel(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.CancelCheckout(r.Context(), models.DefaultWorkspaceID, r.URL.Query().Get("purchase_id"))
	if err != nil {
		h.renderSalesError(w, r, err)
		return
	}
	if result.Cookie != nil {
		http.SetCookie(w, result.Cookie)
	}
	w.Header().Set("Cache-Control", "no-store")
	h.renderSales(w, "sales_cancel", map[string]any{
		"Purchase": result.Purchase, "Flipbook": result.Flipbook,
		"ViewerURL": "/v/" + result.Flipbook.Slug, "CheckoutURL": "/sales/checkout/" + result.Flipbook.ID,
		"Paid": result.Purchase.Status == models.PurchasePaid,
	})
}

func (h *SalesHandler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		salesJSONError(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	if err := h.service.HandleWebhook(r.Context(), payload, r.Header.Get("Stripe-Signature")); err != nil {
		salesJSONError(w, sales.PublicMessage(err), sales.HTTPStatus(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	salesJSONResponse(w, map[string]bool{"received": true})
}

func (h *SalesHandler) renderSales(w http.ResponseWriter, name string, data any) {
	if h.tmpl == nil {
		http.Error(w, "sales templates are not configured", http.StatusInternalServerError)
		return
	}
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "could not render sales page", http.StatusInternalServerError)
	}
}

func (h *SalesHandler) renderSalesError(w http.ResponseWriter, r *http.Request, err error) {
	status := sales.HTTPStatus(err)
	w.Header().Set("Cache-Control", "no-store")
	if h.tmpl != nil && h.tmpl.Lookup("sales_error") != nil {
		w.WriteHeader(status)
		if executeErr := h.tmpl.ExecuteTemplate(w, "sales_error", map[string]any{
			"Status": status, "Message": sales.PublicMessage(err),
		}); executeErr == nil {
			return
		}
		return
	}
	http.Error(w, sales.PublicMessage(err), status)
}

func salesJSONResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func salesJSONError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
