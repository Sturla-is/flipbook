package handlers

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/analytics"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type analyticsAdminIdentity struct {
	UserID      string
	WorkspaceID string
	Role        models.Role
}

type analyticsInputError struct{ err error }

func (e *analyticsInputError) Error() string { return e.err.Error() }
func (e *analyticsInputError) Unwrap() error { return e.err }

type analyticsReportResult struct {
	identity     analyticsAdminIdentity
	report       analytics.Report
	publications []analytics.Publication
	selectedIDs  []string
	workspace    *models.Workspace
	events       []*models.AnalyticsEvent
}

func (h *AnalyticsHandler) Report(w http.ResponseWriter, r *http.Request) {
	result, err := h.buildAnalyticsReport(r)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Cookie, Authorization")
	analyticsJSONResponse(w, result.report)
}

func (h *AnalyticsHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	result, err := h.buildAnalyticsReport(r)
	if err != nil {
		status, message := analyticsAdminErrorDetails(err)
		http.Error(w, message, status)
		return
	}
	if h.tmpl == nil {
		http.Error(w, "analytics templates are not configured", http.StatusServiceUnavailable)
		return
	}
	from, to := result.report.Range.QueryDates()
	consentPolicy, retentionDays := h.options.ConsentPolicy, int(h.options.Retention/(24*time.Hour))
	if result.workspace != nil {
		consentPolicy = result.workspace.Settings.AnalyticsConsentPolicy
		retentionDays = result.workspace.Settings.AnalyticsRetentionDays
	}
	data := analyticsPageData{
		Report: result.report, Publications: result.publications, SelectedFlipbook: strings.Join(result.selectedIDs, ","),
		From: from, To: to, ExportQuery: analyticsExportQuery(r, result),
		CSRFToken: auth.CSRFTokenFromContext(r.Context()), CanManage: result.identity.Role.CanManage(),
		Workspace: result.workspace, Notice: analyticsPrivacyNotice(r.URL.Query().Get("privacy")),
		Breakdowns: analyticsBreakdownTables(result.report), ConsentPolicy: consentPolicy, RetentionDays: retentionDays,
	}
	var output bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&output, "admin_analytics", data); err != nil {
		log.Printf("analytics template failed: %v", err)
		http.Error(w, "could not render analytics", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	_, _ = output.WriteTo(w)
}

func (h *AnalyticsHandler) PublicationDashboard(w http.ResponseWriter, r *http.Request) {
	h.Dashboard(w, r)
}

type analyticsPageData struct {
	Report           analytics.Report
	Publications     []analytics.Publication
	SelectedFlipbook string
	From             string
	To               string
	ExportQuery      string
	CSRFToken        string
	CanManage        bool
	Workspace        *models.Workspace
	Notice           string
	Breakdowns       []analyticsBreakdownTable
	ConsentPolicy    string
	RetentionDays    int
}

type analyticsBreakdownTable struct {
	Title     string
	Dimension string
	Items     []analytics.Breakdown
}

func analyticsBreakdownTables(report analytics.Report) []analyticsBreakdownTable {
	titles := []struct{ title, dimension string }{
		{"Referrers", "referrers"}, {"Devices", "devices"}, {"Browsers", "browsers"},
		{"Countries", "countries"}, {"UTM sources", "utm_sources"}, {"UTM mediums", "utm_mediums"},
		{"UTM campaigns", "utm_campaigns"}, {"UTM content", "utm_contents"}, {"UTM terms", "utm_terms"},
		{"Embed placement", "placements"}, {"Calls to action", "ctas"}, {"Link destinations", "link_hosts"},
		{"Download formats", "download_formats"}, {"Search terms", "search_terms"}, {"Share channels", "share_channels"},
	}
	result := make([]analyticsBreakdownTable, 0, len(titles))
	for _, item := range titles {
		result = append(result, analyticsBreakdownTable{Title: item.title, Dimension: item.dimension, Items: report.Breakdowns[item.dimension]})
	}
	return result
}

func (h *AnalyticsHandler) buildAnalyticsReport(r *http.Request) (*analyticsReportResult, error) {
	identity, err := h.analyticsAdminIdentity(r, models.RoleReader)
	if err != nil {
		return nil, err
	}
	reportRange, err := analytics.ParseRange(r, h.now())
	if err != nil {
		return nil, &analyticsInputError{err: err}
	}
	publications, err := h.analyticsPublications(r.Context(), identity.WorkspaceID)
	if err != nil {
		return nil, err
	}
	selectedIDs, err := analyticsSelectedFlipbooks(r)
	if err != nil {
		return nil, &analyticsInputError{err: err}
	}
	byID := make(map[string]analytics.Publication, len(publications))
	for _, publication := range publications {
		byID[publication.ID] = publication
	}
	scoped := publications
	if len(selectedIDs) > 0 {
		scoped = make([]analytics.Publication, 0, len(selectedIDs))
		for _, id := range selectedIDs {
			publication, ok := byID[id]
			if !ok {
				if _, err := h.store.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id); err != nil {
					return nil, err
				}
				return nil, &analyticsInputError{err: errors.New("selected publication is unavailable")}
			}
			scoped = append(scoped, publication)
		}
	}
	filter := models.AnalyticsFilter{WorkspaceID: identity.WorkspaceID, From: reportRange.From, To: reportRange.To}
	if len(selectedIDs) > 0 {
		filter.FlipbookIDs = selectedIDs
	}
	events, err := analytics.LoadEvents(r.Context(), h.store, filter)
	if err != nil {
		return nil, err
	}
	legacy := make(map[string]int64, len(scoped))
	for _, publication := range scoped {
		legacy[publication.ID] = int64(h.store.GetViewCount(publication.ID))
	}
	workspace, err := h.store.GetWorkspace(r.Context(), identity.WorkspaceID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return nil, err
	}
	report := analytics.BuildReport(analytics.ReportInput{
		WorkspaceID: identity.WorkspaceID, FlipbookID: analyticsSingleSelection(selectedIDs), Range: reportRange,
		Publications: scoped, Events: events, LegacyViews: legacy, Now: h.now(),
	})
	return &analyticsReportResult{identity: identity, report: report, publications: publications, selectedIDs: selectedIDs, workspace: workspace, events: events}, nil
}

func (h *AnalyticsHandler) analyticsPublications(ctx context.Context, workspaceID string) ([]analytics.Publication, error) {
	result := make([]analytics.Publication, 0)
	var offset int64
	for {
		items, total, err := h.store.SearchFlipbooks(ctx, database.FlipbookFilter{WorkspaceID: workspaceID, Sort: "title", Limit: 500, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, fb := range items {
			result = append(result, analytics.Publication{ID: fb.ID, Title: fb.Title, Slug: fb.Slug, PageCount: fb.PageCount})
		}
		offset += int64(len(items))
		if len(items) == 0 || offset >= total {
			return result, nil
		}
	}
}

func (h *AnalyticsHandler) analyticsAdminIdentity(r *http.Request, required models.Role) (analyticsAdminIdentity, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.User == nil || !principal.User.Active() {
		return analyticsAdminIdentity{}, auth.ErrUnauthorized
	}
	workspaceID := chi.URLParam(r, "workspaceID")
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	}
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	}
	if workspaceID == "" {
		workspaceID = principal.WorkspaceID()
	}
	if workspaceID == "" {
		workspaceID = models.DefaultWorkspaceID
	}
	if principal.APIKey != nil {
		if principal.APIKey.WorkspaceID != workspaceID || !principal.APIKey.Active(h.now()) {
			return analyticsAdminIdentity{}, auth.ErrForbidden
		}
		if required == models.RoleAdmin {
			if !principal.APIKey.HasScope(models.ScopeWorkspaceAdmin) {
				return analyticsAdminIdentity{}, auth.ErrForbidden
			}
		} else if !principal.APIKey.HasScope(models.ScopeAnalyticsRead) {
			return analyticsAdminIdentity{}, auth.ErrForbidden
		}
	}
	membership := principal.Membership
	if membership == nil || membership.WorkspaceID != workspaceID {
		var err error
		membership, err = h.store.GetMembership(r.Context(), workspaceID, principal.User.ID)
		if errors.Is(err, database.ErrNotFound) {
			return analyticsAdminIdentity{}, auth.ErrForbidden
		}
		if err != nil {
			return analyticsAdminIdentity{}, err
		}
	}
	if membership.UserID != principal.User.ID || !membership.Role.AtLeast(required) {
		return analyticsAdminIdentity{}, auth.ErrForbidden
	}
	return analyticsAdminIdentity{UserID: principal.User.ID, WorkspaceID: workspaceID, Role: membership.Role}, nil
}

func (h *AnalyticsHandler) UpdatePrivacySettings(w http.ResponseWriter, r *http.Request) {
	identity, err := h.analyticsAdminIdentity(r, models.RoleAdmin)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	var input struct {
		ConsentPolicy string `json:"consent_policy"`
		RetentionDays int    `json:"retention_days"`
	}
	jsonRequest := strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
	if jsonRequest {
		if err := decodeAnalyticsJSON(w, r, &input, 4096); err != nil {
			analyticsJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			analyticsJSONError(w, "invalid form request", http.StatusBadRequest)
			return
		}
		input.ConsentPolicy = r.FormValue("consent_policy")
		input.RetentionDays, err = strconv.Atoi(strings.TrimSpace(r.FormValue("retention_days")))
		if err != nil {
			input.RetentionDays = 0
		}
	}
	input.ConsentPolicy = strings.ToLower(strings.TrimSpace(input.ConsentPolicy))
	switch input.ConsentPolicy {
	case analytics.ConsentRequired, analytics.ConsentAnonymous, analytics.ConsentDisabled:
	default:
		analyticsJSONError(w, "consent_policy must be required, anonymous, or disabled", http.StatusBadRequest)
		return
	}
	if input.RetentionDays < 1 || input.RetentionDays > 3650 {
		analyticsJSONError(w, "retention_days must be between 1 and 3650", http.StatusBadRequest)
		return
	}
	workspace, err := h.store.GetWorkspace(r.Context(), identity.WorkspaceID)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	workspace.Settings.AnalyticsConsentPolicy = input.ConsentPolicy
	workspace.Settings.AnalyticsRetentionDays = input.RetentionDays
	if err := h.store.UpdateWorkspaceSettings(r.Context(), workspace); err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if jsonRequest {
		analyticsJSONResponse(w, map[string]any{"ok": true, "consent_policy": input.ConsentPolicy, "retention_days": input.RetentionDays})
		return
	}
	http.Redirect(w, r, "/admin/analytics?privacy=saved", http.StatusSeeOther)
}

func analyticsSelectedFlipbooks(r *http.Request) ([]string, error) {
	values := make([]string, 0)
	for _, raw := range []string{chi.URLParam(r, "id"), r.URL.Query().Get("flipbook_id"), r.URL.Query().Get("flipbook_ids")} {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if len(value) > 200 || strings.ContainsAny(value, "\x00\r\n") {
				return nil, errors.New("invalid flipbook_id")
			}
			duplicate := false
			for _, existing := range values {
				if existing == value {
					duplicate = true
					break
				}
			}
			if !duplicate {
				values = append(values, value)
			}
		}
	}
	if len(values) > 100 {
		return nil, errors.New("at most 100 publications may be selected")
	}
	return values, nil
}

func analyticsSingleSelection(selected []string) string {
	if len(selected) == 1 {
		return selected[0]
	}
	return ""
}

func analyticsExportQuery(r *http.Request, result *analyticsReportResult) string {
	values := r.URL.Query()
	from, to := result.report.Range.QueryDates()
	values.Set("from", from)
	values.Set("to", to)
	values.Del("flipbook_id")
	values.Del("flipbook_ids")
	if len(result.selectedIDs) == 1 {
		values.Set("flipbook_id", result.selectedIDs[0])
	} else if len(result.selectedIDs) > 1 {
		values.Set("flipbook_ids", strings.Join(result.selectedIDs, ","))
	}
	return values.Encode()
}

func analyticsPrivacyNotice(code string) string {
	if code == "saved" {
		return "Analytics privacy settings were saved."
	}
	return ""
}

func writeAnalyticsAdminError(w http.ResponseWriter, err error) {
	status, message := analyticsAdminErrorDetails(err)
	if status >= http.StatusInternalServerError {
		log.Printf("analytics request failed: %v", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	analyticsJSONError(w, message, status)
}

func analyticsAdminErrorDetails(err error) (int, string) {
	var inputError *analyticsInputError
	if errors.As(err, &inputError) {
		return http.StatusBadRequest, inputError.Error()
	}
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		return http.StatusUnauthorized, "authentication required"
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, database.ErrForbidden):
		return http.StatusForbidden, "insufficient permissions"
	case errors.Is(err, database.ErrNotFound), errors.Is(err, mongo.ErrNoDocuments):
		return http.StatusNotFound, "publication not found"
	case errors.Is(err, analytics.ErrResultTooLarge):
		return http.StatusRequestEntityTooLarge, err.Error()
	case errors.Is(err, analytics.ErrRangeTooLarge):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "analytics query timed out"
	case err != nil:
		return http.StatusInternalServerError, "analytics report is unavailable"
	default:
		return http.StatusOK, ""
	}
}
