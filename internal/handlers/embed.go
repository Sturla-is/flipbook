package handlers

import (
	"html/template"
	"net/http"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/publishing"
	"github.com/metavert-io/flipbook/internal/saas"
	"github.com/metavert-io/flipbook/internal/storage"
)

type EmbedHandler struct {
	viewer *ViewerHandler
}

func NewEmbedHandler(db *database.DB, store *storage.Storage, tmpl *template.Template, baseURL string, services ...*publishing.Service) *EmbedHandler {
	return &EmbedHandler{viewer: NewViewerHandler(db, store, tmpl, baseURL, services...)}
}

func (h *EmbedHandler) SetPublishing(service *publishing.Service) *EmbedHandler {
	h.viewer.SetPublishing(service)
	return h
}

// SetBilling enables billing status checks for tenant-owned flipbooks.
func (h *EmbedHandler) SetBilling(bc *saas.BillingChecker) {
	h.viewer.SetBilling(bc)
}

func (h *EmbedHandler) Embed(w http.ResponseWriter, r *http.Request) {
	h.viewer.serve(w, r, true)
}
