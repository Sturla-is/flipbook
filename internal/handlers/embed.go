package handlers

import (
	"html/template"
	"net/http"

	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/publishing"
	"github.com/jonradoff/flipbook/internal/storage"
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

func (h *EmbedHandler) Embed(w http.ResponseWriter, r *http.Request) {
	h.viewer.serve(w, r, true)
}
