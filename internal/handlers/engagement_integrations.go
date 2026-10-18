package handlers

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/auth"
	"github.com/metavert-io/flipbook/internal/engagement"
	"github.com/metavert-io/flipbook/internal/models"
)

type adminLeadIntegrationsView struct {
	CRM           []engagement.CRMStatus
	Webhooks      []*models.WebhookSubscription
	Deliveries    []*models.WebhookDelivery
	WebhookStatus engagement.WebhookStatus
	CSRFToken     string
	Notice        string
	Error         string
	NewSecret     string
}

func (h *EngagementHandler) AdminLeadIntegrations(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	data, err := h.leadIntegrationData(r, workspaceID)
	if err != nil {
		http.Error(w, "Integration status could not be loaded", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = auth.CSRFTokenFromContext(r.Context())
	data.Notice = integrationNotice(r.URL.Query().Get("notice"))
	data.Error = integrationError(r.URL.Query().Get("error"))
	h.render(w, http.StatusOK, "admin_lead_integrations", data)
}

func (h *EngagementHandler) AdminCreateWebhook(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	events := append([]string(nil), r.Form["events"]...)
	if custom := strings.TrimSpace(r.FormValue("custom_events")); custom != "" {
		events = append(events, strings.FieldsFunc(custom, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })...)
	}
	result, err := h.service.CreateWebhook(r.Context(), engagement.WebhookInput{
		WorkspaceID: workspaceID, URL: r.FormValue("url"), Events: events, Secret: r.FormValue("secret"),
	})
	if err != nil {
		http.Redirect(w, r, "/admin/leads/integrations?error=webhook", http.StatusSeeOther)
		return
	}
	data, err := h.leadIntegrationData(r, workspaceID)
	if err != nil {
		http.Error(w, "Webhook was created but the status page could not be reloaded", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = auth.CSRFTokenFromContext(r.Context())
	data.Notice = "Webhook created. " + result.Subscription.ID
	if result.Secret != "" {
		data.NewSecret = result.Secret
	}
	h.render(w, http.StatusCreated, "admin_lead_integrations", data)
}

func (h *EngagementHandler) AdminSetWebhookEnabled(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	subscriptionID := chi.URLParam(r, "subscriptionID")
	if subscriptionID == "" {
		subscriptionID = r.FormValue("subscription_id")
	}
	enabled := r.FormValue("enabled") == "true" || formBool(r, "enable")
	if err := h.service.SetWebhookEnabled(r.Context(), workspaceID, subscriptionID, enabled); err != nil {
		http.Redirect(w, r, "/admin/leads/integrations?error=webhook-state", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/leads/integrations?notice=webhook-state", http.StatusSeeOther)
}

func (h *EngagementHandler) leadIntegrationData(r *http.Request, workspaceID string) (*adminLeadIntegrationsView, error) {
	webhooks, err := h.service.Webhooks(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	deliveries, err := h.service.WebhookDeliveries(r.Context(), workspaceID, 100)
	if err != nil {
		return nil, err
	}
	return &adminLeadIntegrationsView{
		CRM: h.service.CRMStatus(), Webhooks: webhooks, Deliveries: deliveries,
		WebhookStatus: h.service.WebhookStatus(),
	}, nil
}

func integrationNotice(code string) string {
	messages := map[string]string{"webhook-state": "Webhook state updated."}
	return messages[code]
}

func integrationError(code string) string {
	messages := map[string]string{
		"webhook":       "Webhook settings are invalid or webhooks are not configured. Use an HTTPS URL, at least one event, and a secret of at least 32 bytes when overriding the default.",
		"webhook-state": "Webhook state could not be updated.",
	}
	return messages[code]
}
