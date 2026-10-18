package handlers

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/integrations"
	"github.com/jonradoff/flipbook/internal/models"
)

type IntegrationsHandler struct {
	service *integrations.Service
	tmpl    *template.Template
}

func NewIntegrationsHandler(service *integrations.Service, tmpl *template.Template) *IntegrationsHandler {
	return &IntegrationsHandler{service: service, tmpl: tmpl}
}

func NewIntegrationHandler(service *integrations.Service, tmpl *template.Template) *IntegrationsHandler {
	return NewIntegrationsHandler(service, tmpl)
}

func (h *IntegrationsHandler) RegisterRoutes(router chi.Router) {
	router.Get("/admin/integrations", h.Page)
	router.Get("/admin/integrations/{provider}/connect", h.Connect)
	router.Get("/admin/integrations/{provider}/callback", h.Callback)
	router.Post("/admin/integrations/{provider}/verify", h.Verify)
	router.Post("/admin/integrations/{provider}/disconnect", h.Disconnect)
	router.Post("/admin/integrations/{provider}/export", h.Export)
	router.Get("/admin/integrations/{provider}/statistics", h.Statistics)
	router.Post("/admin/integrations/webhooks", h.CreateWebhook)
	router.Post("/admin/integrations/webhooks/test", h.TestWebhook)
	router.Post("/admin/integrations/webhooks/{id}", h.SetWebhookEnabled)
	router.Get("/admin/integrations/webhooks/{id}/deliveries", h.Deliveries)
}

type integrationProviderView struct {
	Info         integrations.ProviderInfo
	Connection   *models.IntegrationConnection
	Available    bool
	Connected    bool
	StatusLabel  string
	LastVerified string
}

type integrationsPageData struct {
	CSRFToken        string
	Providers        []integrationProviderView
	CanManageWebhook bool
	Webhooks         []*models.WebhookSubscription
	Deliveries       []*models.WebhookDelivery
	Notice           string
	Warning          string
	Error            string
	WebhookSecret    string
	Statistics       *integrations.Statistics
	StatisticsJSON   string
	ExportResult     *integrations.ExportResult
}

func integrationPrincipal(request *http.Request, required models.Role) (*auth.Principal, error) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	if !ok || principal.User == nil || !principal.User.Active() {
		return nil, auth.ErrUnauthorized
	}
	if principal.WorkspaceID() == "" || !principal.Role().AtLeast(required) {
		return nil, auth.ErrForbidden
	}
	return principal, nil
}

func integrationTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02 15:04 UTC")
}

func (h *IntegrationsHandler) pageData(request *http.Request) (*integrationsPageData, error) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		return nil, err
	}
	workspaceID := principal.WorkspaceID()
	connections, err := h.service.Connections(request.Context(), workspaceID, principal.User.ID)
	if err != nil {
		return nil, err
	}
	data := &integrationsPageData{
		CSRFToken:        auth.CSRFTokenFromContext(request.Context()),
		CanManageWebhook: principal.Role().AtLeast(models.RoleAdmin),
		Notice:           request.URL.Query().Get("notice"), Warning: request.URL.Query().Get("warning"),
	}
	for _, info := range h.service.ProviderCatalog() {
		view := integrationProviderView{Info: info, StatusLabel: "Not connected"}
		for _, connection := range connections {
			if connection.Provider != info.Name {
				continue
			}
			if view.Connection == nil || connection.UpdatedAt.After(view.Connection.UpdatedAt) {
				view.Connection = connection
			}
		}
		if view.Connection != nil {
			view.Available = view.Connection.Status != models.IntegrationDisconnected
			view.Connected = view.Connection.Status == models.IntegrationConnected
			view.StatusLabel = strings.ReplaceAll(string(view.Connection.Status), "_", " ")
			view.LastVerified = integrationTime(view.Connection.LastVerifiedAt)
		}
		data.Providers = append(data.Providers, view)
	}
	if data.CanManageWebhook {
		data.Webhooks, err = h.service.Webhooks(request.Context(), workspaceID)
		if err != nil {
			return nil, err
		}
		data.Deliveries, err = h.service.WebhookDeliveries(request.Context(), workspaceID, "", 50)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (h *IntegrationsHandler) render(writer http.ResponseWriter, request *http.Request, status int, data *integrationsPageData) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	writer.WriteHeader(status)
	if err := h.tmpl.ExecuteTemplate(writer, "admin_integrations", data); err != nil {
		return
	}
}

func (h *IntegrationsHandler) renderError(writer http.ResponseWriter, request *http.Request, err error) {
	status, code, message := integrationErrorDetails(err)
	data, dataErr := h.pageData(request)
	if dataErr != nil {
		writeIntegrationJSON(writer, status, map[string]any{"error": message, "code": code})
		return
	}
	data.Error = message
	h.render(writer, request, status, data)
}

func integrationErrorDetails(err error) (int, string, string) {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Sign in to manage integrations."
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "forbidden", "You do not have permission to manage this integration."
	case errors.Is(err, database.ErrNotFound):
		return http.StatusNotFound, "not_found", "The integration resource was not found."
	default:
		return integrations.HTTPStatus(err), integrations.ErrorCode(err), integrations.PublicMessage(err)
	}
}

func writeIntegrationJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeIntegrationError(writer http.ResponseWriter, err error) {
	status, code, message := integrationErrorDetails(err)
	writeIntegrationJSON(writer, status, map[string]any{"error": message, "code": code})
}

func integrationWantsJSON(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Accept")), "application/json") ||
		request.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

func (h *IntegrationsHandler) fail(writer http.ResponseWriter, request *http.Request, err error) {
	if integrationWantsJSON(request) {
		writeIntegrationError(writer, err)
		return
	}
	h.renderError(writer, request, err)
}

func integrationRedirectURL(notice, warning string) string {
	query := url.Values{}
	if notice != "" {
		query.Set("notice", notice)
	}
	if warning != "" {
		query.Set("warning", warning)
	}
	destination := "/admin/integrations"
	if encoded := query.Encode(); encoded != "" {
		destination += "?" + encoded
	}
	return destination
}

func (h *IntegrationsHandler) Page(writer http.ResponseWriter, request *http.Request) {
	data, err := h.pageData(request)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	h.render(writer, request, http.StatusOK, data)
}

func (h *IntegrationsHandler) Connect(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	session, err := h.service.BeginOAuth(request.Context(), integrations.OAuthRequest{
		Provider: chi.URLParam(request, "provider"), WorkspaceID: principal.WorkspaceID(), UserID: principal.User.ID,
	})
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: integrations.OAuthBindingCookieName, Value: session.Binding, Path: "/admin/integrations",
		HttpOnly: true, Secure: h.service.SecureCookies(), SameSite: http.SameSiteLaxMode,
		Expires: session.ExpiresAt, MaxAge: 600,
	})
	http.Redirect(writer, request, session.AuthorizationURL, http.StatusFound)
}

func (h *IntegrationsHandler) Callback(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	binding := ""
	if cookie, cookieErr := request.Cookie(integrations.OAuthBindingCookieName); cookieErr == nil {
		binding = cookie.Value
	}
	defer func() {
		http.SetCookie(writer, &http.Cookie{
			Name: integrations.OAuthBindingCookieName, Value: "", Path: "/admin/integrations",
			HttpOnly: true, Secure: h.service.SecureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: -1,
		})
	}()
	provider := chi.URLParam(request, "provider")
	connection, err := h.service.CompleteOAuth(request.Context(), integrations.OAuthCallback{
		Provider: provider, WorkspaceID: principal.WorkspaceID(), UserID: principal.User.ID,
		State: request.URL.Query().Get("state"), Code: request.URL.Query().Get("code"),
		Binding: binding, Error: request.URL.Query().Get("error"),
	})
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusOK, map[string]any{"connection": connection})
		return
	}
	http.Redirect(writer, request, integrationRedirectURL(provider+" connection authorized.", ""), http.StatusSeeOther)
}

func (h *IntegrationsHandler) Verify(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	provider := chi.URLParam(request, "provider")
	connection, err := h.service.VerifyConnection(request.Context(), principal.WorkspaceID(), principal.User.ID, provider)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusOK, map[string]any{"connection": connection, "verified": true})
		return
	}
	http.Redirect(writer, request, integrationRedirectURL(provider+" connection verified.", ""), http.StatusSeeOther)
}

func (h *IntegrationsHandler) Disconnect(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	provider := chi.URLParam(request, "provider")
	result, err := h.service.Disconnect(request.Context(), principal.WorkspaceID(), principal.User.ID, provider)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusOK, result)
		return
	}
	http.Redirect(writer, request, integrationRedirectURL(provider+" disconnected.", result.Warning), http.StatusSeeOther)
}

func (h *IntegrationsHandler) Export(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if err := request.ParseForm(); err != nil {
		h.fail(writer, request, err)
		return
	}
	result, err := h.service.ExportAndReupload(request.Context(), integrations.ExportRequest{
		Provider: chi.URLParam(request, "provider"), WorkspaceID: principal.WorkspaceID(), UserID: principal.User.ID,
		ExternalID: request.FormValue("external_id"), Title: request.FormValue("title"), Pages: request.FormValue("pages"),
	})
	if err != nil {
		if integrationWantsJSON(request) {
			status, code, message := integrationErrorDetails(err)
			writeIntegrationJSON(writer, status, map[string]any{"error": message, "code": code, "result": result})
			return
		}
		data, dataErr := h.pageData(request)
		if dataErr != nil {
			h.fail(writer, request, dataErr)
			return
		}
		data.Error = integrations.PublicMessage(err)
		data.ExportResult = result
		h.render(writer, request, integrations.HTTPStatus(err), data)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusCreated, result)
		return
	}
	http.Redirect(writer, request, result.AdminURL, http.StatusSeeOther)
}

func (h *IntegrationsHandler) Statistics(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleEditor)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	provider := chi.URLParam(request, "provider")
	statistics, err := h.service.Statistics(request.Context(), principal.WorkspaceID(), principal.User.ID, provider, request.URL.Query().Get("external_id"))
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusOK, statistics)
		return
	}
	data, err := h.pageData(request)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	data.Statistics = statistics
	if encoded, marshalErr := json.MarshalIndent(statistics, "", "  "); marshalErr == nil {
		data.StatisticsJSON = string(encoded)
	}
	data.Notice = "Latest provider document statistics and metadata for " + provider + "."
	h.render(writer, request, http.StatusOK, data)
}

func (h *IntegrationsHandler) CreateWebhook(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleAdmin)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if err := request.ParseForm(); err != nil {
		h.fail(writer, request, err)
		return
	}
	events := strings.FieldsFunc(request.FormValue("events"), func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	result, err := h.service.CreateWebhook(request.Context(), principal.WorkspaceID(), request.FormValue("url"), events)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusCreated, result)
		return
	}
	data, err := h.pageData(request)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	data.WebhookSecret = result.Secret
	data.Notice = "Webhook created. Copy the signing secret now; it will not be shown again."
	h.render(writer, request, http.StatusCreated, data)
}

func (h *IntegrationsHandler) SetWebhookEnabled(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleAdmin)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if err := request.ParseForm(); err != nil {
		h.fail(writer, request, err)
		return
	}
	enabledValue := request.FormValue("enabled")
	if enabledValue != "true" && enabledValue != "false" {
		h.fail(writer, request, integrations.ErrInvalidInput)
		return
	}
	if err := h.service.SetWebhookEnabled(request.Context(), principal.WorkspaceID(), chi.URLParam(request, "id"), enabledValue == "true"); err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusOK, map[string]any{"id": chi.URLParam(request, "id"), "enabled": enabledValue == "true"})
		return
	}
	http.Redirect(writer, request, integrationRedirectURL("Webhook subscription updated.", ""), http.StatusSeeOther)
}

func (h *IntegrationsHandler) TestWebhook(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleAdmin)
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	queued, err := h.service.EmitWebhook(request.Context(), principal.WorkspaceID(), "integration.test", "", map[string]any{
		"message": "Integration webhook test", "requested_by": principal.User.ID,
	})
	if err != nil {
		h.fail(writer, request, err)
		return
	}
	if integrationWantsJSON(request) {
		writeIntegrationJSON(writer, http.StatusAccepted, map[string]any{"event": "integration.test", "queued": queued})
		return
	}
	http.Redirect(writer, request, integrationRedirectURL("Test event queued for signed delivery.", ""), http.StatusSeeOther)
}

func (h *IntegrationsHandler) Deliveries(writer http.ResponseWriter, request *http.Request) {
	principal, err := integrationPrincipal(request, models.RoleAdmin)
	if err != nil {
		writeIntegrationError(writer, err)
		return
	}
	deliveries, err := h.service.WebhookDeliveries(request.Context(), principal.WorkspaceID(), chi.URLParam(request, "id"), 100)
	if err != nil {
		writeIntegrationError(writer, err)
		return
	}
	writeIntegrationJSON(writer, http.StatusOK, map[string]any{"deliveries": deliveries})
}
