package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func (h *AdminHandler) Collections(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderCollections(w, r, actor, http.StatusOK, nil)
}

func (h *AdminHandler) renderCollections(w http.ResponseWriter, r *http.Request, actor adminActor, status int, overrides map[string]any) {
	collections, err := h.db.ListCollections(r.Context(), actor.WorkspaceID, "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	data := map[string]any{"PageTitle": "Collections - Penninn Midlun", "Collections": collections}
	for key, value := range overrides {
		data[key] = value
	}
	if err := h.render(w, r, actor, "collections", "admin_collections", status, data); err != nil {
		h.renderError(w, r, err)
	}
}

func (h *AdminHandler) CreateCollection(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderCollections(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The collection form is invalid."})
		return
	}
	collection := &models.Collection{WorkspaceID: actor.WorkspaceID, CreatedBy: actor.UserID, FlipbookIDs: []string{}}
	if err := applyAdminCollectionForm(collection, r); err != nil {
		h.renderCollections(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	if err := h.db.CreateCollection(r.Context(), collection); err != nil {
		h.collectionError(w, r, actor, err)
		return
	}
	h.audit(r, actor, "collection.create", "collection", collection.ID, "success", nil)
	http.Redirect(w, r, "/admin/collections?notice="+url.QueryEscape("Collection created."), http.StatusSeeOther)
}

func (h *AdminHandler) UpdateCollection(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderCollections(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The collection form is invalid."})
		return
	}
	id := chi.URLParam(r, "id")
	collection, err := h.db.GetCollection(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := applyAdminCollectionForm(collection, r); err != nil {
		h.renderCollections(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	if collection.CoverFlipbookID != "" {
		if _, err := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, collection.CoverFlipbookID); err != nil {
			h.renderCollections(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The cover publication is invalid."})
			return
		}
	}
	if err := h.db.UpdateCollection(r.Context(), collection); err != nil {
		h.collectionError(w, r, actor, err)
		return
	}
	h.audit(r, actor, "collection.update", "collection", id, "success", nil)
	http.Redirect(w, r, "/admin/collections?notice="+url.QueryEscape("Collection updated."), http.StatusSeeOther)
}

func applyAdminCollectionForm(collection *models.Collection, r *http.Request) error {
	collection.Name = strings.TrimSpace(r.FormValue("name"))
	collection.Slug = models.NormalizeSlug(r.FormValue("slug"))
	if collection.Slug == "" {
		collection.Slug = models.NormalizeSlug(collection.Name)
	}
	collection.Description = strings.TrimSpace(r.FormValue("description"))
	collection.CoverFlipbookID = strings.TrimSpace(r.FormValue("cover_flipbook_id"))
	collection.Visibility = models.Visibility(r.FormValue("visibility"))
	collection.SEO.Title = strings.TrimSpace(r.FormValue("seo_title"))
	collection.SEO.Description = strings.TrimSpace(r.FormValue("seo_description"))
	collection.SEO.NoIndex = adminFormBool(r, "seo_noindex")
	if collection.Name == "" || len(collection.Name) > 200 || collection.Slug == "" || len(collection.Slug) > 200 {
		return adminInputf("Collection name and slug are required and must be at most 200 characters.")
	}
	if len(collection.Description) > 5000 {
		return adminInputf("Collection description must be at most 5,000 characters.")
	}
	switch collection.Visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted, models.VisibilityPrivate, models.VisibilityTeam:
	default:
		return adminInputf("Choose a valid collection visibility.")
	}
	return nil
}

func (h *AdminHandler) collectionError(w http.ResponseWriter, r *http.Request, actor adminActor, err error) {
	status, message := adminErrorMessage(err)
	if mongo.IsDuplicateKeyError(err) {
		status = http.StatusConflict
		message = "That collection slug is already in use."
	}
	h.renderCollections(w, r, actor, status, map[string]any{"Error": message})
}

func (h *AdminHandler) DeleteCollection(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.db.DeleteCollection(r.Context(), actor.WorkspaceID, id); err != nil {
		h.collectionError(w, r, actor, err)
		return
	}
	h.audit(r, actor, "collection.delete", "collection", id, "success", nil)
	http.Redirect(w, r, "/admin/collections?notice="+url.QueryEscape("Collection deleted. Publications were not deleted."), http.StatusSeeOther)
}
