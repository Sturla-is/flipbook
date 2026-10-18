package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
)

func (h *AdminHandler) Security(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderSecurity(w, r, actor, http.StatusOK, nil)
}

func (h *AdminHandler) renderSecurity(w http.ResponseWriter, r *http.Request, actor adminActor, status int, overrides map[string]any) {
	keys, err := h.db.ListAPIKeys(r.Context(), actor.WorkspaceID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	query := r.URL.Query()
	from, err := parseAdminTime(query.Get("from"), "Audit start time")
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	to, err := parseAdminTime(query.Get("to"), "Audit end time")
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	limit, err := adminQueryInteger(query.Get("limit"), 50, 1, 200)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	filter := database.AuditFilter{
		WorkspaceID: actor.WorkspaceID, ResourceType: strings.TrimSpace(query.Get("resource_type")),
		ResourceID: strings.TrimSpace(query.Get("resource_id")), Action: strings.TrimSpace(query.Get("action")), Limit: int64(limit),
	}
	if from != nil {
		filter.From = *from
	}
	if to != nil {
		filter.To = *to
	}
	events, err := h.db.ListAuditEvents(r.Context(), filter)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	filters := map[string]string{
		"action": query.Get("action"), "resource_type": query.Get("resource_type"),
		"resource_id": query.Get("resource_id"), "from": query.Get("from"), "to": query.Get("to"),
	}
	data := map[string]any{
		"PageTitle": "API keys and audit - Flipbook", "TeamSection": "security", "APIKeys": keys,
		"AuditEvents": events, "AuditFilters": filters, "AuditLimit": limit,
		"Scopes": []string{
			models.ScopeFlipbooksRead, models.ScopeFlipbooksWrite, models.ScopeAnalyticsRead,
			models.ScopeLeadsRead, models.ScopeLeadsWrite, models.ScopeSalesRead,
			models.ScopeIntegrationsWrite, models.ScopeWorkspaceAdmin,
		},
	}
	for key, value := range overrides {
		data[key] = value
	}
	if err := h.render(w, r, actor, "team", "admin_security", status, data); err != nil {
		h.renderError(w, r, err)
	}
}

func (h *AdminHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderSecurity(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "API-key service wiring is required."})
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderSecurity(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The API-key form is invalid."})
		return
	}
	expiresAt, err := parseAdminTime(r.FormValue("expires_at"), "API-key expiry")
	if err != nil {
		h.renderSecurity(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	secret, key, err := h.auth.IssueAPIKey(r.Context(), actor.WorkspaceID, actor.UserID, r.FormValue("name"), r.Form["scopes"], expiresAt)
	if err != nil {
		status, message := adminErrorMessage(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadRequest
			message = "The API key could not be created. Check its name, scopes, and expiry."
		}
		h.renderSecurity(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	h.renderSecurity(w, r, actor, http.StatusCreated, map[string]any{
		"Notice":          "API key created. Copy the secret now; it will not be shown again.",
		"NewAPIKeySecret": secret, "NewAPIKey": key,
	})
}

func (h *AdminHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderSecurity(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "API-key service wiring is required."})
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.auth.RevokeAPIKey(r.Context(), actor.WorkspaceID, actor.UserID, id); err != nil {
		status, message := adminErrorMessage(err)
		h.renderSecurity(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	http.Redirect(w, r, "/admin/security?notice="+url.QueryEscape("API key revoked."), http.StatusSeeOther)
}
