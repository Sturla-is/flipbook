package handlers

import (
	"bytes"
	"mime"
	"net/http"

	"github.com/jonradoff/flipbook/internal/analytics"
)

func (h *AnalyticsHandler) ExportReportJSON(w http.ResponseWriter, r *http.Request) {
	result, err := h.buildAnalyticsReport(r)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	h.analyticsDownloadHeaders(w, "application/json; charset=utf-8", analytics.ExportFilename("analytics-report", "json", result.report))
	analyticsJSONResponse(w, result.report)
}

func (h *AnalyticsHandler) ExportReportCSV(w http.ResponseWriter, r *http.Request) {
	result, err := h.buildAnalyticsReport(r)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	var output bytes.Buffer
	if err := analytics.WriteReportCSV(&output, result.report); err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	h.analyticsDownloadHeaders(w, "text/csv; charset=utf-8", analytics.ExportFilename("analytics-report", "csv", result.report))
	_, _ = output.WriteTo(w)
}

func (h *AnalyticsHandler) ExportEventsJSON(w http.ResponseWriter, r *http.Request) {
	result, err := h.buildAnalyticsReport(r)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	h.analyticsDownloadHeaders(w, "application/json; charset=utf-8", analytics.ExportFilename("analytics-events", "json", result.report))
	analyticsJSONResponse(w, map[string]any{
		"range": result.report.Range, "scope": result.report.Scope,
		"count": len(result.events), "events": analytics.SanitizeEvents(result.events),
	})
}

func (h *AnalyticsHandler) ExportEventsCSV(w http.ResponseWriter, r *http.Request) {
	result, err := h.buildAnalyticsReport(r)
	if err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	var output bytes.Buffer
	if err := analytics.WriteEventsCSV(&output, result.events); err != nil {
		writeAnalyticsAdminError(w, err)
		return
	}
	h.analyticsDownloadHeaders(w, "text/csv; charset=utf-8", analytics.ExportFilename("analytics-events", "csv", result.report))
	_, _ = output.WriteTo(w)
}

func (h *AnalyticsHandler) analyticsDownloadHeaders(w http.ResponseWriter, contentType, filename string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Cookie, Authorization")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
}
