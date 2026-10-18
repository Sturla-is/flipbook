package handlers

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type collectionRequest struct {
	Name            *string             `json:"name"`
	Slug            *string             `json:"slug"`
	Description     *string             `json:"description"`
	Visibility      *models.Visibility  `json:"visibility"`
	FlipbookIDs     []string            `json:"flipbook_ids"`
	CoverFlipbookID *string             `json:"cover_flipbook_id"`
	SEO             *models.SEOMetadata `json:"seo"`
}

func (h *APIHandler) ListCollections(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	collections, err := h.db.ListCollections(r.Context(), identity.WorkspaceID, models.Visibility(r.URL.Query().Get("visibility")))
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"items": collections})
}

func (h *APIHandler) CreateCollection(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request collectionRequest
	if err := decodeAPIJSON(r, &request); err != nil || request.Name == nil || strings.TrimSpace(*request.Name) == "" {
		invalidAPIRequest(w, "collection name is required")
		return
	}
	collection := &models.Collection{WorkspaceID: identity.WorkspaceID, CreatedBy: identity.UserID}
	applyCollectionRequest(collection, request)
	if collection.Visibility == "" {
		collection.Visibility = models.VisibilityPrivate
	}
	if !validAPICollectionVisibility(collection.Visibility) {
		invalidAPIRequest(w, "unsupported collection visibility")
		return
	}
	if err := h.db.CreateCollection(r.Context(), collection); err != nil {
		apiError(w, err)
		return
	}
	for _, flipbookID := range request.FlipbookIDs {
		if err := h.db.AddFlipbookToCollection(r.Context(), identity.WorkspaceID, collection.ID, flipbookID); err != nil {
			apiError(w, err)
			return
		}
	}
	collection, err = h.db.GetCollection(r.Context(), identity.WorkspaceID, collection.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "collection.create", "collection", collection.ID, nil)
	jsonStatus(w, http.StatusCreated, collection)
}

func applyCollectionRequest(collection *models.Collection, request collectionRequest) {
	if request.Name != nil {
		collection.Name = strings.TrimSpace(*request.Name)
	}
	if request.Slug != nil {
		collection.Slug = models.NormalizeSlug(*request.Slug)
	}
	if request.Description != nil {
		collection.Description = *request.Description
	}
	if request.Visibility != nil {
		collection.Visibility = *request.Visibility
	}
	if request.CoverFlipbookID != nil {
		collection.CoverFlipbookID = *request.CoverFlipbookID
	}
	if request.SEO != nil {
		collection.SEO = *request.SEO
	}
}

func validAPICollectionVisibility(visibility models.Visibility) bool {
	switch visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted, models.VisibilityPrivate, models.VisibilityTeam:
		return true
	default:
		return false
	}
}

func (h *APIHandler) GetCollection(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	collection, err := h.apiCollection(r, identity)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, collection)
}

func (h *APIHandler) apiCollection(r *http.Request, identity *apiIdentity) (*models.Collection, error) {
	id := chi.URLParam(r, "collectionID")
	collection, err := h.db.GetCollection(r.Context(), identity.WorkspaceID, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return h.db.GetCollectionBySlug(r.Context(), identity.WorkspaceID, id)
	}
	return collection, err
}

func (h *APIHandler) PatchCollection(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request collectionRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		invalidAPIRequest(w, "Invalid collection: "+err.Error())
		return
	}
	collection, err := h.apiCollection(r, identity)
	if err != nil {
		apiError(w, err)
		return
	}
	applyCollectionRequest(collection, request)
	if collection.Name == "" || !validAPICollectionVisibility(collection.Visibility) {
		invalidAPIRequest(w, "collection name and valid visibility are required")
		return
	}
	if request.CoverFlipbookID != nil && collection.CoverFlipbookID != "" {
		if _, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, collection.CoverFlipbookID); err != nil {
			apiError(w, err)
			return
		}
	}
	if err := h.db.UpdateCollection(r.Context(), collection); err != nil {
		apiError(w, err)
		return
	}
	if request.FlipbookIDs != nil {
		desired := make(map[string]struct{}, len(request.FlipbookIDs))
		for _, id := range request.FlipbookIDs {
			desired[id] = struct{}{}
		}
		for _, id := range collection.FlipbookIDs {
			if _, ok := desired[id]; !ok {
				if err := h.db.RemoveFlipbookFromCollection(r.Context(), identity.WorkspaceID, collection.ID, id); err != nil {
					apiError(w, err)
					return
				}
			}
		}
		for id := range desired {
			if err := h.db.AddFlipbookToCollection(r.Context(), identity.WorkspaceID, collection.ID, id); err != nil {
				apiError(w, err)
				return
			}
		}
	}
	h.audit(r, identity, "collection.update", "collection", collection.ID, nil)
	collection, err = h.db.GetCollection(r.Context(), identity.WorkspaceID, collection.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, collection)
}

func (h *APIHandler) DeleteCollection(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	collection, err := h.apiCollection(r, identity)
	if err != nil {
		apiError(w, err)
		return
	}
	if err := h.db.DeleteCollection(r.Context(), identity.WorkspaceID, collection.ID); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "collection.delete", "collection", collection.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandler) AddFlipbookToCollection(w http.ResponseWriter, r *http.Request) {
	h.mutateCollectionMembership(w, r, true)
}

func (h *APIHandler) RemoveFlipbookFromCollection(w http.ResponseWriter, r *http.Request) {
	h.mutateCollectionMembership(w, r, false)
}

func (h *APIHandler) mutateCollectionMembership(w http.ResponseWriter, r *http.Request, add bool) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	collection, err := h.apiCollection(r, identity)
	if err != nil {
		apiError(w, err)
		return
	}
	flipbookID := chi.URLParam(r, "id")
	if add {
		err = h.db.AddFlipbookToCollection(r.Context(), identity.WorkspaceID, collection.ID, flipbookID)
	} else {
		err = h.db.RemoveFlipbookFromCollection(r.Context(), identity.WorkspaceID, collection.ID, flipbookID)
	}
	if err != nil {
		apiError(w, err)
		return
	}
	collection, err = h.db.GetCollection(r.Context(), identity.WorkspaceID, collection.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, collection)
}

type tagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

func (h *APIHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	items, total, err := h.db.SearchFlipbooks(r.Context(), database.FlipbookFilter{WorkspaceID: identity.WorkspaceID, Limit: 500})
	if err != nil {
		apiError(w, err)
		return
	}
	counts := make(map[string]int)
	for _, fb := range items {
		for _, tag := range fb.Tags {
			counts[tag]++
		}
	}
	result := make([]tagCount, 0, len(counts))
	for tag, count := range counts {
		result = append(result, tagCount{Tag: tag, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count == result[j].Count {
			return result[i].Tag < result[j].Tag
		}
		return result[i].Count > result[j].Count
	})
	jsonResponse(w, map[string]any{"items": result, "truncated": total > int64(len(items))})
}

type tagsRequest struct {
	Tags   []string `json:"tags"`
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

func (h *APIHandler) UpdateFlipbookTags(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request tagsRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		invalidAPIRequest(w, "Invalid tags request: "+err.Error())
		return
	}
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, err)
		return
	}
	tags := fb.Tags
	var tagErr error
	if request.Tags != nil {
		tags, tagErr = normalizeAPITags(request.Tags)
	} else {
		if len(request.Add) > 0 {
			tags, tagErr = mergeAPITags(tags, request.Add, true)
		}
		if tagErr == nil && len(request.Remove) > 0 {
			tags, tagErr = mergeAPITags(tags, request.Remove, false)
		}
	}
	if tagErr != nil {
		invalidAPIRequest(w, tagErr.Error())
		return
	}
	fb.Tags = tags
	if err := h.db.UpdateFlipbookMetadata(r.Context(), fb); err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"id": fb.ID, "tags": fb.Tags})
}

func (h *APIHandler) DeleteFlipbookTag(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, err)
		return
	}
	fb.Tags, err = mergeAPITags(fb.Tags, []string{chi.URLParam(r, "tag")}, false)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	if err := h.db.UpdateFlipbookMetadata(r.Context(), fb); err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"id": fb.ID, "tags": fb.Tags})
}

type linkRequest struct {
	RevisionID *string          `json:"revision_id"`
	Page       *int             `json:"page"`
	X          *float64         `json:"x"`
	Y          *float64         `json:"y"`
	Width      *float64         `json:"width"`
	Height     *float64         `json:"height"`
	Type       *models.LinkType `json:"type"`
	URL        *string          `json:"url"`
	TargetPage *int             `json:"target_page"`
	Title      *string          `json:"title"`
	Enabled    *bool            `json:"enabled"`
}

func applyLinkRequest(link *models.LinkOverlay, request linkRequest, create bool) {
	if create {
		link.Enabled = true
	}
	if request.RevisionID != nil {
		link.RevisionID = *request.RevisionID
	}
	if request.Page != nil {
		link.Page = *request.Page
	}
	if request.X != nil {
		link.X = *request.X
	}
	if request.Y != nil {
		link.Y = *request.Y
	}
	if request.Width != nil {
		link.Width = *request.Width
	}
	if request.Height != nil {
		link.Height = *request.Height
	}
	if request.Type != nil {
		link.Type = *request.Type
	}
	if request.URL != nil {
		link.URL = *request.URL
	}
	if request.TargetPage != nil {
		link.TargetPage = *request.TargetPage
	}
	if request.Title != nil {
		link.Title = *request.Title
	}
	if request.Enabled != nil {
		link.Enabled = *request.Enabled
	}
}

func (h *APIHandler) ListLinkOverlays(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id); err != nil {
		apiError(w, err)
		return
	}
	links, err := h.db.ListLinkOverlaysForEditor(r.Context(), identity.WorkspaceID, id, r.URL.Query().Get("revision_id"))
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"items": links})
}

func (h *APIHandler) CreateLinkOverlay(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request linkRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		invalidAPIRequest(w, "Invalid link overlay: "+err.Error())
		return
	}
	link := &models.LinkOverlay{WorkspaceID: identity.WorkspaceID, FlipbookID: chi.URLParam(r, "id")}
	applyLinkRequest(link, request, true)
	if err := h.db.CreateLinkOverlay(r.Context(), link); err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	h.audit(r, identity, "flipbook.link.create", "link", link.ID, nil)
	jsonStatus(w, http.StatusCreated, link)
}

func (h *APIHandler) PatchLinkOverlay(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request linkRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		invalidAPIRequest(w, "Invalid link overlay: "+err.Error())
		return
	}
	link, err := h.apiLink(r, identity)
	if err != nil {
		apiError(w, err)
		return
	}
	applyLinkRequest(link, request, false)
	if err := h.db.UpdateLinkOverlay(r.Context(), link); err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	h.audit(r, identity, "flipbook.link.update", "link", link.ID, nil)
	jsonResponse(w, link)
}

func (h *APIHandler) apiLink(r *http.Request, identity *apiIdentity) (*models.LinkOverlay, error) {
	links, err := h.db.ListLinkOverlaysForEditor(r.Context(), identity.WorkspaceID, chi.URLParam(r, "id"), "")
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.ID == chi.URLParam(r, "linkID") {
			return link, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (h *APIHandler) DeleteLinkOverlay(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	link, err := h.apiLink(r, identity)
	if err != nil {
		apiError(w, err)
		return
	}
	if err := h.db.DeleteLinkOverlay(r.Context(), identity.WorkspaceID, link.FlipbookID, link.ID); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.link.delete", "link", link.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}
