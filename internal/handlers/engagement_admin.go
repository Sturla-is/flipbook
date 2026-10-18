package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
)

type adminLeadsView struct {
	Flipbooks          []*models.Flipbook
	SelectedFlipbook   *models.Flipbook
	SelectedFlipbookID string
	Leads              []*models.Lead
	Subscribers        []*models.Subscriber
	Total              int64
	Page               int
	PageCount          int
	PreviousURL        string
	NextURL            string
	ExportURL          string
	From               string
	To                 string
	RetentionDays      int
	CSRFToken          string
	Notice             string
	Error              string
}

func (h *EngagementHandler) AdminLeads(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	filter, page, from, to, err := leadFilterFromRequest(r, workspaceID, true)
	if err != nil {
		http.Error(w, "Invalid lead filters", http.StatusBadRequest)
		return
	}
	leads, err := h.service.Leads(r.Context(), filter)
	if err != nil {
		http.Error(w, "Leads could not be loaded", http.StatusInternalServerError)
		return
	}
	total, err := h.service.LeadCount(r.Context(), workspaceID, filter.FlipbookID)
	if err != nil {
		http.Error(w, "Lead count could not be loaded", http.StatusInternalServerError)
		return
	}
	flipbooks, err := h.service.Flipbooks(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Publications could not be loaded", http.StatusInternalServerError)
		return
	}
	subscribers, err := h.service.Subscribers(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Subscribers could not be loaded", http.StatusInternalServerError)
		return
	}
	retention, err := h.service.LeadRetentionDays(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Retention settings could not be loaded", http.StatusInternalServerError)
		return
	}
	var selected *models.Flipbook
	for _, fb := range flipbooks {
		if fb.ID == filter.FlipbookID {
			selected = fb
			break
		}
	}
	pageCount := int((total + int64(filter.Limit) - 1) / int64(filter.Limit))
	if pageCount < 1 {
		pageCount = 1
	}
	baseQuery := leadFilterQuery(filter.FlipbookID, from, to)
	data := &adminLeadsView{
		Flipbooks: flipbooks, SelectedFlipbook: selected, SelectedFlipbookID: filter.FlipbookID,
		Leads: leads, Subscribers: subscribers, Total: total, Page: page, PageCount: pageCount,
		From: from, To: to, RetentionDays: retention, CSRFToken: auth.CSRFTokenFromContext(r.Context()),
		Notice: adminLeadNotice(r.URL.Query().Get("notice")), Error: adminLeadError(r.URL.Query().Get("error")),
	}
	if page > 1 {
		data.PreviousURL = "/admin/leads?" + withLeadPage(baseQuery, page-1)
	}
	if page < pageCount {
		data.NextURL = "/admin/leads?" + withLeadPage(baseQuery, page+1)
	}
	data.ExportURL = "/admin/leads/export.csv?" + baseQuery.Encode()
	h.render(w, http.StatusOK, "admin_leads", data)
}

func (h *EngagementHandler) AdminExportLeads(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	filter, _, _, _, err := leadFilterFromRequest(r, workspaceID, false)
	if err != nil {
		http.Error(w, "Invalid lead filters", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="leads-%s.csv"`, time.Now().UTC().Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := h.service.ExportLeadsCSV(r.Context(), w, filter); err != nil {
		return
	}
}

func (h *EngagementHandler) AdminUpdateLeadGate(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RolePublisher)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	flipbookID := chi.URLParam(r, "id")
	if flipbookID == "" {
		flipbookID = strings.TrimSpace(r.FormValue("flipbook_id"))
	}
	previewPages, err := strconv.Atoi(strings.TrimSpace(r.FormValue("preview_pages")))
	if err != nil && r.FormValue("preview_pages") != "" {
		http.Error(w, "Preview pages must be a number", http.StatusBadRequest)
		return
	}
	settings := models.LeadGateSettings{
		Enabled: formBool(r, "enabled"), GateReading: formBool(r, "gate_reading"),
		GateDownloads: formBool(r, "gate_downloads"), PreviewPages: previewPages,
		ConsentText:    strings.TrimSpace(r.FormValue("consent_text")),
		SuccessMessage: strings.TrimSpace(r.FormValue("success_message")),
	}
	switch r.FormValue("mode") {
	case "mandatory":
		settings.Mandatory = true
	case "dismissible":
		settings.Dismissible = true
	}
	if settings.Enabled && settings.ConsentText == "" {
		settings.ConsentText = "I consent to provide my email address for access and related communication."
	}
	if _, err := h.service.UpdateLeadGate(r.Context(), workspaceID, flipbookID, settings); err != nil {
		redirectAdminLeadError(w, r, flipbookID, "lead-gate")
		return
	}
	http.Redirect(w, r, "/admin/leads?"+leadFilterQuery(flipbookID, "", "").Encode()+"&notice=gate-saved", http.StatusSeeOther)
}

func (h *EngagementHandler) AdminSetLeadRetention(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	days, err := strconv.Atoi(strings.TrimSpace(r.FormValue("retention_days")))
	if err != nil {
		http.Error(w, "Retention days must be a number", http.StatusBadRequest)
		return
	}
	if err := h.service.SetLeadRetentionDays(r.Context(), workspaceID, days, formBool(r, "apply_existing")); err != nil {
		redirectAdminLeadError(w, r, r.FormValue("flipbook_id"), "retention")
		return
	}
	http.Redirect(w, r, "/admin/leads?"+leadFilterQuery(r.FormValue("flipbook_id"), "", "").Encode()+"&notice=retention-saved", http.StatusSeeOther)
}

func (h *EngagementHandler) AdminDeleteLead(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	leadID := chi.URLParam(r, "leadID")
	if leadID == "" {
		leadID = r.FormValue("lead_id")
	}
	if err := h.service.DeleteLead(r.Context(), workspaceID, leadID); err != nil {
		redirectAdminLeadError(w, r, r.FormValue("flipbook_id"), "delete")
		return
	}
	http.Redirect(w, r, "/admin/leads?"+leadFilterQuery(r.FormValue("flipbook_id"), "", "").Encode()+"&notice=lead-deleted", http.StatusSeeOther)
}

func (h *EngagementHandler) AdminDeleteFlipbookLeads(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	flipbookID := strings.TrimSpace(r.FormValue("flipbook_id"))
	if err := h.service.DeleteLeadsForFlipbook(r.Context(), workspaceID, flipbookID); err != nil {
		redirectAdminLeadError(w, r, flipbookID, "delete")
		return
	}
	http.Redirect(w, r, "/admin/leads?notice=leads-deleted", http.StatusSeeOther)
}

func (h *EngagementHandler) AdminDeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireEngagementRole(w, r, models.RoleAdmin)
	if !ok {
		return
	}
	if !parseFormLimited(w, r) {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	subscriberID := chi.URLParam(r, "subscriberID")
	if subscriberID == "" {
		subscriberID = r.FormValue("subscriber_id")
	}
	if err := h.service.DeleteSubscriber(r.Context(), workspaceID, subscriberID); err != nil {
		redirectAdminLeadError(w, r, "", "delete")
		return
	}
	http.Redirect(w, r, "/admin/leads?notice=subscriber-deleted", http.StatusSeeOther)
}

func requireEngagementRole(w http.ResponseWriter, r *http.Request, required models.Role) (string, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.User == nil {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return "", false
	}
	workspaceID := principal.WorkspaceID()
	if requested := chi.URLParam(r, "workspaceID"); requested != "" && requested != workspaceID {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return "", false
	}
	if workspaceID == "" || !principal.Role().AtLeast(required) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return "", false
	}
	return workspaceID, true
}

func leadFilterFromRequest(r *http.Request, workspaceID string, paginated bool) (database.LeadFilter, int, string, string, error) {
	filter := database.LeadFilter{WorkspaceID: workspaceID, FlipbookID: strings.TrimSpace(r.URL.Query().Get("flipbook_id"))}
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	var err error
	if from != "" {
		filter.From, err = time.Parse("2006-01-02", from)
		if err != nil {
			return filter, 0, from, to, err
		}
	}
	if to != "" {
		filter.To, err = time.Parse("2006-01-02", to)
		if err != nil {
			return filter, 0, from, to, err
		}
		filter.To = filter.To.Add(24 * time.Hour)
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		return filter, 0, from, to, fmt.Errorf("invalid date range")
	}
	page := 1
	if paginated {
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, err = strconv.Atoi(raw)
			if err != nil || page < 1 || page > 100000 {
				return filter, 0, from, to, fmt.Errorf("invalid page")
			}
		}
		filter.Limit = 100
		filter.Offset = int64(page-1) * filter.Limit
	}
	return filter, page, from, to, nil
}

func leadFilterQuery(flipbookID, from, to string) url.Values {
	query := make(url.Values)
	if flipbookID != "" {
		query.Set("flipbook_id", flipbookID)
	}
	if from != "" {
		query.Set("from", from)
	}
	if to != "" {
		query.Set("to", to)
	}
	return query
}

func withLeadPage(query url.Values, page int) string {
	clone := make(url.Values, len(query))
	for key, values := range query {
		clone[key] = append([]string(nil), values...)
	}
	clone.Set("page", strconv.Itoa(page))
	return clone.Encode()
}

func redirectAdminLeadError(w http.ResponseWriter, r *http.Request, flipbookID, code string) {
	http.Redirect(w, r, "/admin/leads?"+leadFilterQuery(flipbookID, "", "").Encode()+"&error="+url.QueryEscape(code), http.StatusSeeOther)
}

func adminLeadNotice(code string) string {
	messages := map[string]string{
		"gate-saved": "Lead gate settings saved.", "retention-saved": "Lead retention settings saved.",
		"lead-deleted": "Lead deleted.", "leads-deleted": "Publication leads deleted.",
		"subscriber-deleted": "Subscriber deleted.",
	}
	return messages[code]
}

func adminLeadError(code string) string {
	messages := map[string]string{
		"lead-gate": "Lead gate settings are invalid. Check the mode, gated resources, and preview page count.",
		"retention": "Retention settings are invalid or could not be saved.",
		"delete":    "The requested records could not be deleted.",
	}
	return messages[code]
}
