package handlers

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/saas"
	"github.com/metavert-io/flipbook/internal/storage"
)

type EmbedHandler struct {
	db      *database.DB
	storage *storage.Storage
	tmpl    *template.Template
	baseURL string
	billing *saas.BillingChecker
}

func NewEmbedHandler(db *database.DB, store *storage.Storage, tmpl *template.Template, baseURL string) *EmbedHandler {
	return &EmbedHandler{db: db, storage: store, tmpl: tmpl, baseURL: baseURL}
}

// SetBilling enables billing status checks for tenant-owned flipbooks.
func (h *EmbedHandler) SetBilling(bc *saas.BillingChecker) {
	h.billing = bc
}

func (h *EmbedHandler) Embed(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	fb, err := h.db.GetFlipbookBySlug(slug)
	if err != nil {
		http.Error(w, "Flipbook not found", 404)
		return
	}

	w.Header().Set("X-Frame-Options", "ALLOWALL")
	w.Header().Set("Content-Security-Policy", "frame-ancestors *")

	// Billing check for tenant-owned flipbooks
	if fb.TenantID != "" && h.billing != nil {
		if !h.billing.TenantCanServe(r.Context(), fb.TenantID) {
			h.tmpl.ExecuteTemplate(w, "unavailable", map[string]interface{}{
				"Flipbook": fb,
				"BaseURL":  h.baseURL,
			})
			return
		}
	}

	switch fb.Status {
	case models.StatusReady:
		// continue below
	case models.StatusRegenerating, models.StatusConverting, models.StatusPending:
		h.tmpl.ExecuteTemplate(w, "viewer_wait", map[string]interface{}{
			"Flipbook": fb,
			"BaseURL":  h.baseURL,
		})
		return
	default:
		http.Error(w, "Flipbook not found", 404)
		return
	}

	go h.db.RecordView(fb.ID, r.Referer(), r.UserAgent())

	pageFmt := h.storage.DetectPageFormat(fb.ID)
	var pages []string
	var thumbs []string
	for i := 1; i <= fb.PageCount; i++ {
		pages = append(pages, h.storage.PageImageURL(fb.ID, pageFmt, i))
		thumbs = append(thumbs, h.storage.ThumbImageURL(fb.ID, pageFmt, i))
	}

	pageTexts := h.storage.LoadPageTexts(fb.ID)
	pageTextsJSON, _ := json.Marshal(pageTexts)

	h.tmpl.ExecuteTemplate(w, "embed", map[string]interface{}{
		"Flipbook":      fb,
		"Pages":         pages,
		"Thumbs":        thumbs,
		"PageTexts":     pageTexts,
		"PageTextsJSON": template.JS(pageTextsJSON),
		"BaseURL":       h.baseURL,
	})
}
