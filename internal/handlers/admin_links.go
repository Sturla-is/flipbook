package handlers

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/converter"
	"github.com/metavert-io/flipbook/internal/models"
)

// linkEditorPage describes one page for the admin link hotspot editor: its
// image URLs, geometry, and the links extracted from the PDF at conversion.
type linkEditorPage struct {
	Number       int          `json:"number"`
	Source       string       `json:"src"`
	Thumbnail    string       `json:"thumb"`
	Width        int          `json:"width"`
	Height       int          `json:"height"`
	WidthPoints  float64      `json:"width_points"`
	HeightPoints float64      `json:"height_points"`
	PDFLinks     []readerLink `json:"pdf_links"`
}

// LinkEditor renders the full-page link hotspot editor for a flipbook.
func (h *AdminHandler) LinkEditor(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	flipbook, err := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	overlays, err := h.db.ListLinkOverlaysForEditor(r.Context(), actor.WorkspaceID, id, flipbook.CurrentRevisionID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if overlays == nil {
		overlays = []*models.LinkOverlay{}
	}
	var manifest *converter.Manifest
	if directory, dirErr := readerArtifactDir(h.storage, id, 0); dirErr == nil {
		manifest, _ = converter.LoadManifest(directory)
	}
	format := h.storage.DetectPageFormat(id)
	pageCount := flipbook.PageCount
	if manifest != nil && manifest.PageCount > 0 {
		pageCount = manifest.PageCount
	}
	pages := make([]linkEditorPage, 0, pageCount)
	for number := 1; number <= pageCount; number++ {
		page := linkEditorPage{
			Number: number, Width: flipbook.PageWidth, Height: flipbook.PageHeight,
			WidthPoints: float64(flipbook.PageWidth), HeightPoints: float64(flipbook.PageHeight),
			PDFLinks: []readerLink{},
		}
		if manifest != nil && number <= len(manifest.Pages) {
			manifestPage := &manifest.Pages[number-1]
			page.Width, page.Height = manifestPage.Width, manifestPage.Height
			page.WidthPoints, page.HeightPoints = manifestPage.WidthPoints, manifestPage.HeightPoints
			for _, link := range manifestPage.Links {
				if converted, ok := makeManifestReaderLink(link, page.WidthPoints, page.HeightPoints); ok {
					page.PDFLinks = append(page.PDFLinks, converted)
				}
			}
		}
		if page.Width < 1 {
			page.Width = 1200
		}
		if page.Height < 1 {
			page.Height = 900
		}
		if page.WidthPoints <= 0 {
			page.WidthPoints = float64(page.Width)
		}
		if page.HeightPoints <= 0 {
			page.HeightPoints = float64(page.Height)
		}
		page.Source = h.storage.PageImageURL(id, format, number)
		page.Thumbnail = h.storage.ThumbImageURL(id, format, number)
		pages = append(pages, page)
	}
	payload := map[string]any{
		"flipbook_id": flipbook.ID,
		"title":       flipbook.Title,
		"revision_id": flipbook.CurrentRevisionID,
		"api":         "/api/flipbooks/" + flipbook.ID + "/links",
		"page_count":  pageCount,
		"pages":       pages,
		"overlays":    overlays,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	data := map[string]any{
		"PageTitle": flipbook.Title + " links - Flipbook admin",
		"Flipbook":  flipbook,
		"DataJSON":  template.JS(encoded),
	}
	if err := h.render(w, r, actor, "library", "admin_links", http.StatusOK, data); err != nil {
		h.renderError(w, r, err)
	}
}
