package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/engagement"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type EngagementHandler struct {
	service *engagement.Service
	tmpl    *template.Template
	limiter *engagement.RateLimiter
}

type EngagementHandlerOptions struct {
	RateLimit  int
	RateWindow time.Duration
}

func NewEngagementHandler(service *engagement.Service, tmpl *template.Template) *EngagementHandler {
	return NewEngagementHandlerWithOptions(service, tmpl, EngagementHandlerOptions{})
}

func NewEngagementHandlerWithOptions(service *engagement.Service, tmpl *template.Template, options EngagementHandlerOptions) *EngagementHandler {
	return &EngagementHandler{
		service: service, tmpl: tmpl,
		limiter: engagement.NewRateLimiter(options.RateLimit, options.RateWindow),
	}
}

type leadSubmission struct {
	Email    string            `json:"email"`
	Name     string            `json:"name"`
	Consent  bool              `json:"consent"`
	Source   string            `json:"source"`
	Metadata map[string]string `json:"metadata"`
}

func (h *EngagementHandler) SubmitLead(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "lead-ip") {
		return
	}
	flipbookID := chi.URLParam(r, "id")
	var input leadSubmission
	if isJSONRequest(r) {
		if err := decodeJSONBody(w, r, &input); err != nil {
			h.writeError(w, r, err)
			return
		}
	} else {
		if !parseFormLimited(w, r) {
			h.writeError(w, r, engagement.ErrInvalidInput)
			return
		}
		input = leadSubmission{
			Email: r.FormValue("email"), Name: r.FormValue("name"),
			Consent: formBool(r, "consent"), Source: r.FormValue("source"),
		}
	}
	if !h.allow(w, r, "lead-email", flipbookID, input.Email) {
		return
	}
	result, err := h.service.CaptureLead(r.Context(), engagement.LeadInput{
		FlipbookID: flipbookID, Email: input.Email, Name: input.Name, Consent: input.Consent,
		Source: input.Source, UTM: models.CampaignFromQuery(r.URL.Query()), Metadata: input.Metadata,
		Access: h.accessRequest(r, flipbookID),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if result.Grant != nil && result.Grant.Cookie != nil {
		http.SetCookie(w, result.Grant.Cookie)
	}
	w.Header().Set("Cache-Control", "no-store")
	if wantsJSON(r) {
		response := map[string]any{"ok": true, "message": result.Message}
		if result.Grant != nil {
			response["access_expires_at"] = result.Grant.ExpiresAt
		}
		jsonResponse(w, response)
		return
	}
	h.render(w, http.StatusOK, "engagement_result", map[string]any{
		"Title": "Access unlocked", "Message": result.Message,
		"ContinueURL":   safeReturnURL(r.FormValue("return_to"), "/"),
		"ContinueLabel": "Continue reading",
	})
}

func (h *EngagementHandler) LeadForm(w http.ResponseWriter, r *http.Request) {
	flipbookID := chi.URLParam(r, "id")
	authorization, err := h.service.LeadGate(r.Context(), flipbookID, h.accessRequest(r, flipbookID))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	fb := authorization.Flipbook
	h.render(w, http.StatusOK, "engagement_lead_form", leadFormData(r, &fb, ""))
}

func leadFormData(r *http.Request, fb *models.Flipbook, errorMessage string) map[string]any {
	return map[string]any{
		"Flipbook": fb, "Error": errorMessage, "Email": r.FormValue("email"),
		"CSRFToken":     auth.CSRFTokenFromContext(r.Context()),
		"FormAction":    "/api/public/flipbooks/" + url.PathEscape(fb.ID) + "/lead",
		"DismissAction": "/api/public/flipbooks/" + url.PathEscape(fb.ID) + "/lead/dismiss",
		"ReturnTo":      "/v/" + url.PathEscape(fb.Slug),
	}
}

func (h *EngagementHandler) DismissLeadGate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !h.allow(w, r, "lead-dismiss") {
		return
	}
	flipbookID := chi.URLParam(r, "id")
	grant, err := h.service.DismissLeadGate(r.Context(), flipbookID, h.accessRequest(r, flipbookID))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if grant != nil && grant.Cookie != nil {
		http.SetCookie(w, grant.Cookie)
	}
	w.Header().Set("Cache-Control", "no-store")
	if wantsJSON(r) {
		jsonResponse(w, map[string]any{"ok": true, "access_expires_at": grant.ExpiresAt})
		return
	}
	http.Redirect(w, r, safeReturnURL(r.FormValue("return_to"), "/"), http.StatusSeeOther)
}

func (h *EngagementHandler) accessRequest(r *http.Request, flipbookID string) publishing.AccessRequest {
	request := publishing.AccessRequest{Grant: h.service.GrantFromRequest(r, flipbookID)}
	if user, ok := auth.UserFromContext(r.Context()); ok {
		request.Reader.UserID = user.ID
	}
	return request
}

type subscriptionSubmission struct {
	Email   string `json:"email"`
	Consent bool   `json:"consent"`
	Source  string `json:"source"`
}

func (h *EngagementHandler) SubscribeForm(w http.ResponseWriter, r *http.Request) {
	workspaceID := engagementWorkspaceID(r)
	workspace, err := h.service.Workspace(r.Context(), workspaceID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.render(w, http.StatusOK, "engagement_subscribe_form", map[string]any{
		"Workspace": workspace, "CSRFToken": auth.CSRFTokenFromContext(r.Context()),
		"FormAction": "/api/public/workspaces/" + url.PathEscape(workspaceID) + "/subscriptions",
	})
}

func (h *EngagementHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	workspaceID := engagementWorkspaceID(r)
	if !h.allow(w, r, "subscribe-ip", workspaceID) {
		return
	}
	var input subscriptionSubmission
	if isJSONRequest(r) {
		if err := decodeJSONBody(w, r, &input); err != nil {
			h.writeError(w, r, err)
			return
		}
	} else {
		if !parseFormLimited(w, r) {
			h.writeError(w, r, engagement.ErrInvalidInput)
			return
		}
		input = subscriptionSubmission{Email: r.FormValue("email"), Consent: formBool(r, "consent"), Source: r.FormValue("source")}
	}
	if !h.allow(w, r, "subscribe-email", workspaceID, input.Email) {
		return
	}
	_, err := h.service.Subscribe(r.Context(), engagement.SubscribeInput{
		WorkspaceID: workspaceID, Email: input.Email, Consent: input.Consent, Source: input.Source,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	message := "If your subscription requires confirmation, a confirmation message has been sent. Check your inbox to complete the request."
	if wantsJSON(r) {
		jsonResponse(w, map[string]any{"ok": true, "message": message})
		return
	}
	h.render(w, http.StatusOK, "engagement_result", map[string]any{
		"Title": "Check your inbox", "Message": message,
		"ContinueURL": "/", "ContinueLabel": "Return to the catalog",
	})
}

func (h *EngagementHandler) ConfirmSubscription(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	workspaceID, token := subscriptionTokenRequest(r)
	if !h.allow(w, r, "subscription-confirm", workspaceID) {
		return
	}
	_, err := h.service.ConfirmSubscription(r.Context(), workspaceID, token)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if wantsJSON(r) {
		jsonResponse(w, map[string]any{"ok": true})
		return
	}
	h.render(w, http.StatusOK, "engagement_result", map[string]any{
		"Title": "Subscription confirmed", "Message": "You will now receive new-publication notifications.",
		"ContinueURL": "/", "ContinueLabel": "Return to the catalog",
	})
}

func (h *EngagementHandler) UnsubscribeForm(w http.ResponseWriter, r *http.Request) {
	workspaceID, token := subscriptionTokenRequest(r)
	if len(token) < 32 || len(token) > 512 || workspaceID == "" {
		h.writeError(w, r, engagement.ErrInvalidToken)
		return
	}
	h.render(w, http.StatusOK, "engagement_unsubscribe", map[string]any{
		"WorkspaceID": workspaceID, "Token": token, "CSRFToken": auth.CSRFTokenFromContext(r.Context()),
	})
}

func (h *EngagementHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	workspaceID, token := subscriptionTokenRequest(r)
	if !h.allow(w, r, "unsubscribe", workspaceID) {
		return
	}
	_, err := h.service.Unsubscribe(r.Context(), workspaceID, token)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if wantsJSON(r) {
		jsonResponse(w, map[string]any{"ok": true})
		return
	}
	h.render(w, http.StatusOK, "engagement_result", map[string]any{
		"Title": "Unsubscribed", "Message": "You will no longer receive new-publication notifications for this catalog.",
		"ContinueURL": "/", "ContinueLabel": "Return to the catalog",
	})
}

func engagementWorkspaceID(r *http.Request) string {
	workspaceID := chi.URLParam(r, "workspaceID")
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("workspace_id")
	}
	if workspaceID == "" && r.Method != http.MethodGet {
		_ = r.ParseForm()
		workspaceID = r.FormValue("workspace_id")
	}
	return strings.TrimSpace(workspaceID)
}

func subscriptionTokenRequest(r *http.Request) (string, string) {
	workspaceID := engagementWorkspaceID(r)
	token := r.URL.Query().Get("token")
	if r.Method != http.MethodGet {
		_ = r.ParseForm()
		if value := r.FormValue("token"); value != "" {
			token = value
		}
	}
	return workspaceID, strings.TrimSpace(token)
}

func (h *EngagementHandler) allow(w http.ResponseWriter, r *http.Request, scope string, identifiers ...string) bool {
	parts := []string{scope, directClientAddress(r)}
	parts = append(parts, identifiers...)
	allowed, retryAfter := h.limiter.Allow(parts...)
	if allowed {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
	h.writeError(w, r, engagement.ErrRateLimit)
	return false
}

func directClientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if host == "" {
		return "unknown"
	}
	return host
}

func isJSONRequest(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json")
}

func wantsJSON(r *http.Request) bool {
	return isJSONRequest(r) || strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return engagement.ErrInvalidInput
	}
	if decoder.More() {
		return engagement.ErrInvalidInput
	}
	return nil
}

func parseFormLimited(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	return r.ParseForm() == nil
}

func formBool(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.FormValue(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func safeReturnURL(raw, fallback string) string {
	if fallback == "" {
		fallback = "/"
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return fallback
	}
	return parsed.RequestURI()
}

func (h *EngagementHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := engagementErrorDetails(err)
	w.Header().Set("Cache-Control", "no-store")
	if wantsJSON(r) {
		jsonError(w, message, status)
		return
	}
	if h.tmpl == nil {
		http.Error(w, message, status)
		return
	}
	h.render(w, status, "engagement_result", map[string]any{
		"Title": http.StatusText(status), "Message": message,
		"ContinueURL": "/", "ContinueLabel": "Return",
	})
}

func engagementErrorDetails(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, engagement.ErrInvalidInput):
		return http.StatusBadRequest, "The submitted information is invalid. Check the form and try again."
	case errors.Is(err, engagement.ErrConsentRequired):
		return http.StatusBadRequest, "Consent is required to continue."
	case errors.Is(err, engagement.ErrNotConfigured):
		return http.StatusServiceUnavailable, "This capability is not configured."
	case errors.Is(err, engagement.ErrNotFound), errors.Is(err, mongo.ErrNoDocuments):
		return http.StatusNotFound, "The requested resource was not found."
	case errors.Is(err, engagement.ErrForbidden):
		return http.StatusForbidden, "You do not have permission to perform this action."
	case errors.Is(err, engagement.ErrInvalidToken):
		return http.StatusBadRequest, "This subscription link is invalid or expired. Request a new confirmation message if needed."
	case errors.Is(err, engagement.ErrDelivery):
		return http.StatusBadGateway, "The confirmation message could not be delivered. Try again later."
	case errors.Is(err, engagement.ErrRateLimit):
		return http.StatusTooManyRequests, "Too many requests. Wait and try again."
	}
	var publishingError *publishing.HTTPError
	if errors.As(err, &publishingError) {
		return publishing.HTTPStatus(err), publishing.PublicMessage(err)
	}
	return http.StatusInternalServerError, "The engagement request could not be completed."
}

func (h *EngagementHandler) render(w http.ResponseWriter, status int, name string, data any) {
	if h.tmpl == nil {
		http.Error(w, "Template renderer is not configured", http.StatusServiceUnavailable)
		return
	}
	var output bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&output, name, data); err != nil {
		http.Error(w, "Template rendering failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}
