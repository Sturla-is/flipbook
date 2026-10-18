package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/worker"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/crypto/bcrypt"
)

func (h *APIHandler) ListFlipbooksV1(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	limit, offset, err := apiPage(r, 50, 500)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	query := r.URL.Query()
	filter := database.FlipbookFilter{
		WorkspaceID: identity.WorkspaceID, Query: query.Get("q"),
		Status: query.Get("status"), PublicationState: models.PublicationState(query.Get("publication_state")),
		Visibility: models.Visibility(query.Get("visibility")), Tags: apiStringList(query.Get("tags")),
		CollectionID: query.Get("collection_id"), ExternalSource: query.Get("external_source"),
		ExternalID: query.Get("external_id"), Sort: query.Get("sort"), Limit: limit, Offset: offset,
	}
	if filter.Query == "" {
		filter.Query = query.Get("query")
	}
	if raw := query.Get("archived"); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			invalidAPIRequest(w, "archived must be true or false")
			return
		}
		filter.Archived = &value
	}
	switch query.Get("order") {
	case "", "asc":
	case "desc":
		filter.Descending = true
	default:
		invalidAPIRequest(w, "order must be asc or desc")
		return
	}
	items, total, err := h.db.SearchFlipbooks(r.Context(), filter)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "limit") {
			invalidAPIRequest(w, err.Error())
		} else {
			apiError(w, err)
		}
		return
	}
	result := make([]flipbookDTO, 0, len(items))
	for _, fb := range items {
		result = append(result, h.presentFlipbook(fb, false))
	}
	jsonResponse(w, map[string]any{
		"items":      result,
		"pagination": apiPagination{Limit: limit, Offset: offset, Total: total, HasMore: offset+int64(len(items)) < total},
	})
}

type nullableAPITime struct {
	Set   bool
	Value *time.Time
}

func (n *nullableAPITime) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Value = nil
		return nil
	}
	var value string
	if err := jsonUnmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := apiTime(value)
	if err != nil {
		return err
	}
	n.Value = &parsed
	return nil
}

func jsonUnmarshal(data []byte, value any) error {
	return json.Unmarshal(data, value)
}

type patchFlipbookRequest struct {
	Slug           *string                  `json:"slug"`
	Title          *string                  `json:"title"`
	Description    *string                  `json:"description"`
	Language       *string                  `json:"language"`
	Visibility     *models.Visibility       `json:"visibility"`
	Tags           *[]string                `json:"tags"`
	CollectionIDs  *[]string                `json:"collection_ids"`
	ExternalSource *string                  `json:"external_source"`
	ExternalID     *string                  `json:"external_id"`
	SEO            *models.SEOMetadata      `json:"seo"`
	Branding       *models.BrandingSettings `json:"branding"`
	Download       *models.DownloadSettings `json:"download"`
	Embed          *models.EmbedSettings    `json:"embed"`
	Share          *models.ShareSettings    `json:"share"`
	CTA            *models.CTASettings      `json:"cta"`
	LeadGate       *models.LeadGateSettings `json:"lead_gate"`
	Sales          *models.SalesSettings    `json:"sales"`
	PublishAt      nullableAPITime          `json:"publish_at"`
	ExpiresAt      nullableAPITime          `json:"expires_at"`
	Password       *string                  `json:"password"`
}

func (h *APIHandler) PatchFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request patchFlipbookRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		invalidAPIRequest(w, "Invalid flipbook metadata: "+err.Error())
		return
	}
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, err)
		return
	}
	if request.Title != nil {
		fb.Title = strings.TrimSpace(*request.Title)
		if fb.Title == "" || len(fb.Title) > 500 {
			invalidAPIRequest(w, "title must contain 1 to 500 bytes")
			return
		}
	}
	if request.Description != nil {
		fb.Description = *request.Description
	}
	if request.Language != nil {
		fb.Language = strings.TrimSpace(*request.Language)
		if fb.Language == "" || len(fb.Language) > 35 {
			invalidAPIRequest(w, "language is required and must not exceed 35 bytes")
			return
		}
	}
	if request.Visibility != nil {
		fb.Visibility = *request.Visibility
	}
	switch fb.Visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted, models.VisibilityPrivate, models.VisibilityTeam, models.VisibilityPassword, models.VisibilityToken:
	default:
		invalidAPIRequest(w, "unsupported visibility")
		return
	}
	if request.Tags != nil {
		tags, tagErr := normalizeAPITags(*request.Tags)
		if tagErr != nil {
			invalidAPIRequest(w, tagErr.Error())
			return
		}
		fb.Tags = tags
	}
	if request.CollectionIDs != nil {
		seen := make(map[string]struct{}, len(*request.CollectionIDs))
		fb.CollectionIDs = nil
		for _, collectionID := range *request.CollectionIDs {
			collectionID = strings.TrimSpace(collectionID)
			if collectionID == "" {
				invalidAPIRequest(w, "collection IDs must not be empty")
				return
			}
			if _, ok := seen[collectionID]; ok {
				continue
			}
			if _, err := h.db.GetCollection(r.Context(), identity.WorkspaceID, collectionID); err != nil {
				apiError(w, err)
				return
			}
			seen[collectionID] = struct{}{}
			fb.CollectionIDs = append(fb.CollectionIDs, collectionID)
		}
	}
	if request.ExternalSource != nil {
		fb.ExternalSource = strings.TrimSpace(*request.ExternalSource)
	}
	if request.ExternalID != nil {
		fb.ExternalID = strings.TrimSpace(*request.ExternalID)
	}
	if (fb.ExternalSource == "") != (fb.ExternalID == "") {
		invalidAPIRequest(w, "external_source and external_id must be provided together")
		return
	}
	if request.SEO != nil {
		fb.SEO = *request.SEO
	}
	if request.Branding != nil {
		fb.Branding = *request.Branding
	}
	if request.Download != nil {
		fb.Download = *request.Download
	}
	if request.Embed != nil {
		fb.Embed = *request.Embed
	}
	if request.Share != nil {
		fb.Share = *request.Share
	}
	if request.CTA != nil {
		fb.CTA = *request.CTA
	}
	if request.LeadGate != nil {
		fb.LeadGate = *request.LeadGate
	}
	if request.Sales != nil {
		fb.Sales = *request.Sales
	}
	if request.PublishAt.Set {
		fb.PublishAt = request.PublishAt.Value
	}
	if request.ExpiresAt.Set {
		fb.ExpiresAt = request.ExpiresAt.Value
	}
	if request.Password != nil {
		if *request.Password == "" {
			fb.PasswordHash = ""
		} else {
			if len(*request.Password) < 8 || len(*request.Password) > 72 {
				invalidAPIRequest(w, "password must contain 8 to 72 bytes")
				return
			}
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(*request.Password), 12)
			if hashErr != nil {
				apiError(w, hashErr)
				return
			}
			fb.PasswordHash = string(hash)
		}
	}
	if fb.Visibility == models.VisibilityPassword && fb.PasswordHash == "" {
		invalidAPIRequest(w, "password visibility requires a non-empty password")
		return
	}
	if fb.LeadGate.PreviewPages < 0 || fb.Sales.PreviewPages < 0 || fb.Embed.StartPage < 0 {
		invalidAPIRequest(w, "preview and start page values must not be negative")
		return
	}
	if fb.Sales.Enabled && (fb.Sales.PriceCents <= 0 || len(fb.Sales.Currency) != 3) {
		invalidAPIRequest(w, "paid publications require a positive price and three-letter currency")
		return
	}
	if request.Slug != nil {
		slug := models.NormalizeSlug(*request.Slug)
		if slug == "" || len(slug) > 200 {
			invalidAPIRequest(w, "slug must contain 1 to 200 bytes")
			return
		}
		if err := h.db.ChangeFlipbookSlug(r.Context(), identity.WorkspaceID, fb.ID, slug); err != nil {
			apiError(w, err)
			return
		}
		fb.Slug = slug
	}
	if request.CollectionIDs != nil {
		if err := h.syncFlipbookCollections(r, identity.WorkspaceID, fb); err != nil {
			apiError(w, err)
			return
		}
	}
	if err := h.db.UpdateFlipbookMetadata(r.Context(), fb); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.update", "flipbook", fb.ID, nil)
	h.emitWebhook(r, identity.WorkspaceID, "publication.updated", fb.ID, "", h.presentFlipbook(fb, false))
	updated, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, fb.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, h.presentFlipbook(updated, false))
}

func (h *APIHandler) syncFlipbookCollections(r *http.Request, workspaceID string, fb *models.Flipbook) error {
	current, err := h.db.GetFlipbookInWorkspace(r.Context(), workspaceID, fb.ID)
	if err != nil {
		return err
	}
	desired := make(map[string]struct{}, len(fb.CollectionIDs))
	for _, id := range fb.CollectionIDs {
		desired[id] = struct{}{}
	}
	existing := make(map[string]struct{}, len(current.CollectionIDs))
	for _, id := range current.CollectionIDs {
		existing[id] = struct{}{}
		if _, ok := desired[id]; !ok {
			if err := h.db.RemoveFlipbookFromCollection(r.Context(), workspaceID, id, fb.ID); err != nil {
				return err
			}
		}
	}
	for id := range desired {
		if _, ok := existing[id]; !ok {
			if err := h.db.AddFlipbookToCollection(r.Context(), workspaceID, id, fb.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

type publishRequest struct {
	Visibility models.Visibility `json:"visibility"`
	PublishAt  *time.Time        `json:"publish_at"`
	ExpiresAt  *time.Time        `json:"expires_at"`
}

func (h *APIHandler) PublishFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RolePublisher)
	if err != nil {
		apiError(w, err)
		return
	}
	var request publishRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeAPIJSON(r, &request); err != nil {
			invalidAPIRequest(w, "Invalid publish request: "+err.Error())
			return
		}
	}
	fb, err := h.publishFlipbook(r, identity, chi.URLParam(r, "id"), request)
	if err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.publish", "flipbook", fb.ID, nil)
	eventType := "publication.published"
	if fb.PublicationState == models.PublicationDraft {
		eventType = "publication.scheduled"
	}
	h.emitWebhook(r, identity.WorkspaceID, eventType, fb.ID, "", h.presentFlipbook(fb, false))
	jsonResponse(w, h.presentFlipbook(fb, false))
}

func (h *APIHandler) publishFlipbook(r *http.Request, identity *apiIdentity, id string, request publishRequest) (*models.Flipbook, error) {
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	workspace, err := h.db.GetWorkspace(r.Context(), identity.WorkspaceID)
	if err != nil {
		return nil, err
	}
	now := h.now().UTC()
	if fb.Status != models.StatusReady || fb.PageCount < 1 || fb.ArchivedAt != nil {
		return nil, fmt.Errorf("%w: publication is not ready", database.ErrConflict)
	}
	visibility := request.Visibility
	if visibility == "" {
		visibility = fb.Visibility
	}
	if visibility == "" {
		visibility = workspace.Settings.DefaultVisibility
	}
	switch visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted, models.VisibilityPrivate, models.VisibilityTeam, models.VisibilityPassword, models.VisibilityToken:
	default:
		return nil, apiInvalidError("unsupported visibility")
	}
	if visibility == models.VisibilityPassword && fb.PasswordHash == "" {
		return nil, apiInvalidError("password visibility requires a password before publishing")
	}
	if workspace.Settings.RequirePublicationReview && fb.ReviewState != models.ReviewApproved {
		return nil, fmt.Errorf("%w: publication review approval is required", database.ErrConflict)
	}
	if request.ExpiresAt != nil && !request.ExpiresAt.After(now) {
		return nil, apiInvalidError("publication expiry must be in the future")
	}
	scheduled := request.PublishAt != nil && request.PublishAt.After(now)
	if scheduled && request.ExpiresAt != nil && !request.ExpiresAt.After(*request.PublishAt) {
		return nil, apiInvalidError("publication expiry must be after its scheduled publish time")
	}
	candidate := *fb
	candidate.Visibility = visibility
	candidate.ExpiresAt = request.ExpiresAt
	if scheduled {
		candidate.PublishAt = request.PublishAt
		candidate.PublicationState = models.PublicationDraft
		if err := h.db.SetPublicationState(r.Context(), identity.WorkspaceID, id, models.PublicationDraft, visibility, now); err != nil {
			return nil, err
		}
	} else {
		candidate.PublishAt = nil
		candidate.PublicationState = models.PublicationPublished
		if err := h.db.UpdateFlipbookMetadata(r.Context(), &candidate); err != nil {
			return nil, err
		}
		if err := h.db.SetPublicationState(r.Context(), identity.WorkspaceID, id, models.PublicationPublished, visibility, now); err != nil {
			return nil, err
		}
		candidate.PublishedAt = &now
		candidate.UnpublishedAt = nil
	}
	if err := h.db.UpdateFlipbookMetadata(r.Context(), &candidate); err != nil {
		return nil, err
	}
	candidate.ApplyDefaults()
	if scheduled {
		candidate.UnpublishedAt = &now
	}
	return &candidate, nil
}

func (h *APIHandler) UnpublishFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RolePublisher)
	if err != nil {
		apiError(w, err)
		return
	}
	fb, err := h.unpublishFlipbook(r, identity, chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.unpublish", "flipbook", fb.ID, nil)
	h.emitWebhook(r, identity.WorkspaceID, "publication.unpublished", fb.ID, "", h.presentFlipbook(fb, false))
	jsonResponse(w, h.presentFlipbook(fb, false))
}

func (h *APIHandler) unpublishFlipbook(r *http.Request, identity *apiIdentity, id string) (*models.Flipbook, error) {
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	now := h.now().UTC()
	if err := h.db.SetPublicationState(r.Context(), identity.WorkspaceID, id, models.PublicationUnpublished, fb.Visibility, now); err != nil {
		return nil, err
	}
	candidate := *fb
	candidate.PublicationState = models.PublicationUnpublished
	candidate.UnpublishedAt = &now
	candidate.PublishAt = nil
	candidate.ExpiresAt = nil
	candidate.IsPublic = false
	if err := h.db.UpdateFlipbookMetadata(r.Context(), &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

type reviewRequest struct {
	Note string `json:"note"`
}

func (h *APIHandler) SubmitFlipbookReview(w http.ResponseWriter, r *http.Request) {
	h.reviewFlipbook(w, r, models.ReviewPending)
}

func (h *APIHandler) ApproveFlipbookReview(w http.ResponseWriter, r *http.Request) {
	h.reviewFlipbook(w, r, models.ReviewApproved)
}

func (h *APIHandler) RejectFlipbookReview(w http.ResponseWriter, r *http.Request) {
	h.reviewFlipbook(w, r, models.ReviewRejected)
}

func (h *APIHandler) reviewFlipbook(w http.ResponseWriter, r *http.Request, state models.ReviewState) {
	required := models.RoleAdmin
	if state == models.ReviewPending {
		required = models.RoleEditor
	}
	identity, err := h.identity(r, required)
	if err != nil {
		apiError(w, err)
		return
	}
	var request reviewRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeAPIJSON(r, &request); err != nil {
			invalidAPIRequest(w, "Invalid review request: "+err.Error())
			return
		}
	}
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, err)
		return
	}
	workspace, err := h.db.GetWorkspace(r.Context(), identity.WorkspaceID)
	if err != nil {
		apiError(w, err)
		return
	}
	if state == models.ReviewPending {
		if !workspace.Settings.RequirePublicationReview || fb.Status != models.StatusReady || fb.PublicationState == models.PublicationPublished {
			apiError(w, fmt.Errorf("%w: publication cannot be submitted for review", database.ErrConflict))
			return
		}
		fb.ReviewedBy = ""
		fb.ReviewedAt = nil
	} else {
		if fb.ReviewState != models.ReviewPending {
			apiError(w, fmt.Errorf("%w: publication is not awaiting review", database.ErrConflict))
			return
		}
		now := h.now().UTC()
		fb.ReviewedBy = identity.UserID
		fb.ReviewedAt = &now
	}
	fb.ReviewState = state
	fb.ReviewNote = strings.TrimSpace(request.Note)
	if err := h.db.UpdateFlipbookMetadata(r.Context(), fb); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.review."+string(state), "flipbook", fb.ID, nil)
	jsonResponse(w, h.presentFlipbook(fb, false))
}

type archiveRequest struct {
	Reason string `json:"reason"`
}

func (h *APIHandler) ArchiveFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RolePublisher)
	if err != nil {
		apiError(w, err)
		return
	}
	var request archiveRequest
	if r.Body != nil && r.ContentLength != 0 {
		_ = decodeAPIJSON(r, &request)
	}
	id := chi.URLParam(r, "id")
	if err := h.db.ArchiveFlipbook(r.Context(), identity.WorkspaceID, id, strings.TrimSpace(request.Reason)); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.archive", "flipbook", id, nil)
	h.emitWebhook(r, identity.WorkspaceID, "publication.archived", id, "", map[string]string{"reason": request.Reason})
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandler) RestoreFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RolePublisher)
	if err != nil {
		apiError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.db.RestoreFlipbook(r.Context(), identity.WorkspaceID, id); err != nil {
		apiError(w, err)
		return
	}
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id)
	if err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.restore", "flipbook", id, nil)
	h.emitWebhook(r, identity.WorkspaceID, "publication.restored", id, "", h.presentFlipbook(fb, false))
	jsonResponse(w, h.presentFlipbook(fb, false))
}

func (h *APIHandler) scheduleFlipbookPurge(r *http.Request, identity *apiIdentity, id string) (time.Time, error) {
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id)
	if err != nil {
		return time.Time{}, err
	}
	if err := h.db.SoftDeleteFlipbook(r.Context(), identity.WorkspaceID, id, h.purgeDelay); err != nil {
		return time.Time{}, err
	}
	purgeAfter := h.now().Add(h.purgeDelay).UTC()
	h.audit(r, identity, "flipbook.delete", "flipbook", id, map[string]string{"purge_after": purgeAfter.Format(time.RFC3339)})
	h.emitWebhook(r, identity.WorkspaceID, "publication.deleted", id, "publication.deleted:"+id+":"+strconv.FormatInt(purgeAfter.Unix(), 10), map[string]any{
		"flipbook_id": id, "purge_after": purgeAfter,
	})
	_ = fb
	return purgeAfter, nil
}

type purgeRequest struct {
	Confirm bool `json:"confirm"`
}

func (h *APIHandler) PurgeFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleAdmin)
	if err != nil {
		apiError(w, err)
		return
	}
	var request purgeRequest
	if err := decodeAPIJSON(r, &request); err != nil || !request.Confirm {
		invalidAPIRequest(w, "permanent purge requires {\"confirm\":true}")
		return
	}
	if err := h.purgeFlipbook(r, identity, chi.URLParam(r, "id")); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandler) purgeFlipbook(r *http.Request, identity *apiIdentity, id string) error {
	fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id)
	if errors.Is(err, mongo.ErrNoDocuments) {
		pending, listErr := h.db.ListFlipbooksPendingPurge(r.Context(), h.now().AddDate(100, 0, 0))
		if listErr != nil {
			return listErr
		}
		for _, candidate := range pending {
			if candidate.ID == id && candidate.WorkspaceID == identity.WorkspaceID {
				fb = candidate
				err = nil
				break
			}
		}
	}
	if err != nil {
		return err
	}
	if fb == nil {
		return mongo.ErrNoDocuments
	}
	revisions, err := h.db.ListRevisions(r.Context(), identity.WorkspaceID, id)
	if err != nil {
		return err
	}
	h.emitWebhook(r, identity.WorkspaceID, "publication.purged", id, "publication.purged:"+id, map[string]string{"flipbook_id": id})
	if err := h.storage.DeleteFlipbookContext(r.Context(), id); err != nil {
		return err
	}
	gridFSIDs := make(map[string]struct{})
	if fb.GridFSFileID != "" {
		gridFSIDs[fb.GridFSFileID] = struct{}{}
	}
	for _, revision := range revisions {
		if revision.OriginalGridFSFileID != "" {
			gridFSIDs[revision.OriginalGridFSFileID] = struct{}{}
		}
	}
	for gridFSID := range gridFSIDs {
		if err := h.db.DeleteFromGridFS(r.Context(), gridFSID); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
	}
	if err := h.db.PurgeFlipbook(r.Context(), identity.WorkspaceID, id); err != nil {
		return err
	}
	h.audit(r, identity, "flipbook.purge", "flipbook", id, nil)
	return nil
}

type duplicateRequest struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

func (h *APIHandler) DuplicateFlipbook(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	var request duplicateRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeAPIJSON(r, &request); err != nil {
			invalidAPIRequest(w, "Invalid duplicate request: "+err.Error())
			return
		}
	}
	source, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		apiError(w, err)
		return
	}
	reader, cleanup, err := h.openFlipbookSource(r.Context(), source)
	if err != nil {
		apiError(w, err)
		return
	}
	defer cleanup()
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Copy of " + source.Title
	}
	metadata := uploadMetadata{
		Title: title, Slug: models.NormalizeSlug(request.Slug), Description: source.Description,
		Language: source.Language, Visibility: source.Visibility, Tags: source.Tags, CollectionIDs: source.CollectionIDs,
	}
	duplicate, job, err := h.createFlipbookFromSource(r, identity, source.Filename, reader, metadata)
	if err != nil {
		apiError(w, err)
		return
	}
	duplicate.SEO = source.SEO
	duplicate.Branding = source.Branding
	duplicate.Download = source.Download
	duplicate.Embed = source.Embed
	duplicate.Share = source.Share
	duplicate.CTA = source.CTA
	duplicate.LeadGate = source.LeadGate
	duplicate.Sales = source.Sales
	duplicate.PasswordHash = source.PasswordHash
	if err := h.db.UpdateFlipbookMetadata(r.Context(), duplicate); err != nil {
		apiError(w, err)
		return
	}
	h.audit(r, identity, "flipbook.duplicate", "flipbook", duplicate.ID, map[string]string{"source_id": source.ID})
	h.emitWebhook(r, identity.WorkspaceID, "conversion.queued", duplicate.ID, "conversion.queued:"+duplicate.CurrentRevisionID, h.presentFlipbook(duplicate, false))
	response := map[string]any{"flipbook": h.presentFlipbook(duplicate, false)}
	if job != nil {
		response["job_id"] = job.ID
	}
	jsonStatus(w, http.StatusCreated, response)
}

func (h *APIHandler) openFlipbookSource(ctx context.Context, fb *models.Flipbook) (io.Reader, func(), error) {
	path := h.storage.OriginalPath(fb.ID, strings.ToLower(filepath.Ext(fb.Filename)))
	if file, err := os.Open(path); err == nil {
		return file, func() { _ = file.Close() }, nil
	}
	gridFSID := fb.GridFSFileID
	if gridFSID == "" && fb.CurrentRevisionID != "" {
		if revision, err := h.db.GetCurrentRevision(ctx, fb.WorkspaceID, fb.ID); err == nil {
			gridFSID = revision.OriginalGridFSFileID
		}
	}
	if gridFSID == "" {
		return nil, func() {}, apiInvalidError("original source is unavailable")
	}
	file, err := os.CreateTemp("", "flipbook-duplicate-*")
	if err != nil {
		return nil, func() {}, err
	}
	name := file.Name()
	cleanup := func() { _ = file.Close(); _ = os.Remove(name) }
	if _, err := h.db.DownloadFromGridFS(ctx, gridFSID, file); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return file, cleanup, nil
}

type bulkRequest struct {
	Action       string            `json:"action"`
	IDs          []string          `json:"ids"`
	Visibility   models.Visibility `json:"visibility"`
	PublishAt    *time.Time        `json:"publish_at"`
	ExpiresAt    *time.Time        `json:"expires_at"`
	CollectionID string            `json:"collection_id"`
	Tags         []string          `json:"tags"`
	Reason       string            `json:"reason"`
	Confirm      bool              `json:"confirm"`
}

type bulkResult struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (h *APIHandler) BulkFlipbooks(w http.ResponseWriter, r *http.Request) {
	var request bulkRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		invalidAPIRequest(w, "Invalid bulk request: "+err.Error())
		return
	}
	if len(request.IDs) == 0 || len(request.IDs) > 100 {
		invalidAPIRequest(w, "bulk actions require 1 to 100 flipbook IDs")
		return
	}
	required := models.RoleEditor
	switch request.Action {
	case "publish", "unpublish", "archive", "restore":
		required = models.RolePublisher
	case "delete", "purge":
		required = models.RoleAdmin
	case "move", "add_tags", "remove_tags", "retry":
	default:
		invalidAPIRequest(w, "unsupported bulk action")
		return
	}
	identity, err := h.identity(r, required)
	if err != nil {
		apiError(w, err)
		return
	}
	if request.Action == "purge" && !request.Confirm {
		invalidAPIRequest(w, "bulk permanent purge requires confirm=true")
		return
	}
	results := make([]bulkResult, 0, len(request.IDs))
	succeeded := 0
	for _, id := range request.IDs {
		result := bulkResult{ID: id}
		err := h.executeBulkAction(r, identity, request, id, &result)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.OK = true
			succeeded++
		}
		results = append(results, result)
	}
	h.audit(r, identity, "flipbook.bulk."+request.Action, "flipbook", "", map[string]string{
		"requested": strconv.Itoa(len(request.IDs)), "succeeded": strconv.Itoa(succeeded),
	})
	jsonResponse(w, map[string]any{"action": request.Action, "results": results, "requested": len(request.IDs), "succeeded": succeeded, "failed": len(request.IDs) - succeeded})
}

func (h *APIHandler) executeBulkAction(r *http.Request, identity *apiIdentity, request bulkRequest, id string, result *bulkResult) error {
	switch request.Action {
	case "publish":
		fb, err := h.publishFlipbook(r, identity, id, publishRequest{Visibility: request.Visibility, PublishAt: request.PublishAt, ExpiresAt: request.ExpiresAt})
		if err == nil {
			result.Status = string(fb.PublicationState)
			h.emitWebhook(r, identity.WorkspaceID, "publication.published", id, "", h.presentFlipbook(fb, false))
		}
		return err
	case "unpublish":
		fb, err := h.unpublishFlipbook(r, identity, id)
		if err == nil {
			result.Status = string(fb.PublicationState)
			h.emitWebhook(r, identity.WorkspaceID, "publication.unpublished", id, "", h.presentFlipbook(fb, false))
		}
		return err
	case "archive":
		if err := h.db.ArchiveFlipbook(r.Context(), identity.WorkspaceID, id, request.Reason); err == nil {
			result.Status = "archived"
			h.emitWebhook(r, identity.WorkspaceID, "publication.archived", id, "", nil)
			return nil
		} else {
			return err
		}
	case "restore":
		if err := h.db.RestoreFlipbook(r.Context(), identity.WorkspaceID, id); err == nil {
			result.Status = "active"
			h.emitWebhook(r, identity.WorkspaceID, "publication.restored", id, "", nil)
			return nil
		} else {
			return err
		}
	case "delete":
		purgeAfter, err := h.scheduleFlipbookPurge(r, identity, id)
		if err == nil {
			result.Status = "deleted:" + purgeAfter.Format(time.RFC3339)
		}
		return err
	case "purge":
		if err := h.purgeFlipbook(r, identity, id); err == nil {
			result.Status = "purged"
			return nil
		} else {
			return err
		}
	case "move":
		if request.CollectionID == "" {
			return apiInvalidError("collection_id is required")
		}
		if err := h.db.AddFlipbookToCollection(r.Context(), identity.WorkspaceID, request.CollectionID, id); err == nil {
			result.Status = "moved"
			return nil
		} else {
			return err
		}
	case "add_tags", "remove_tags":
		fb, err := h.db.GetFlipbookInWorkspace(r.Context(), identity.WorkspaceID, id)
		if err != nil {
			return err
		}
		tags, err := mergeAPITags(fb.Tags, request.Tags, request.Action == "add_tags")
		if err != nil {
			return err
		}
		fb.Tags = tags
		if err := h.db.UpdateFlipbookMetadata(r.Context(), fb); err != nil {
			return err
		}
		result.Status = "updated"
		return nil
	case "retry":
		job, err := h.worker.EnqueueContext(r.Context(), worker.Job{FlipbookID: id, WorkspaceID: identity.WorkspaceID})
		if err == nil {
			result.Status = "queued:" + job.ID
		}
		return err
	default:
		return apiInvalidError("unsupported bulk action")
	}
}

func mergeAPITags(current, changes []string, add bool) ([]string, error) {
	normalized, err := normalizeAPITags(changes)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(current)+len(normalized))
	for _, tag := range current {
		set[tag] = struct{}{}
	}
	for _, tag := range normalized {
		if add {
			set[tag] = struct{}{}
		} else {
			delete(set, tag)
		}
	}
	result := make([]string, 0, len(set))
	for tag := range set {
		result = append(result, tag)
	}
	return normalizeAPITags(result)
}

func (h *APIHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	limit, _, err := apiPage(r, 100, 1000)
	if err != nil {
		invalidAPIRequest(w, err.Error())
		return
	}
	jobs, err := h.db.ListJobs(r.Context(), database.JobFilter{
		WorkspaceID: identity.WorkspaceID, FlipbookID: r.URL.Query().Get("flipbook_id"),
		Type: models.JobType(r.URL.Query().Get("type")), Status: models.JobStatus(r.URL.Query().Get("status")), Limit: limit,
	})
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"items": jobs})
}

func (h *APIHandler) GetJob(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	job, err := h.db.GetJob(r.Context(), chi.URLParam(r, "jobID"))
	if err != nil {
		apiError(w, err)
		return
	}
	if job.WorkspaceID != identity.WorkspaceID {
		apiError(w, mongo.ErrNoDocuments)
		return
	}
	jsonResponse(w, job)
}

func (h *APIHandler) RetryJob(w http.ResponseWriter, r *http.Request) {
	h.mutateJob(w, r, true)
}

func (h *APIHandler) CancelJob(w http.ResponseWriter, r *http.Request) {
	h.mutateJob(w, r, false)
}

func (h *APIHandler) mutateJob(w http.ResponseWriter, r *http.Request, retry bool) {
	identity, err := h.identity(r, models.RoleEditor)
	if err != nil {
		apiError(w, err)
		return
	}
	job, err := h.db.GetJob(r.Context(), chi.URLParam(r, "jobID"))
	if err != nil {
		apiError(w, err)
		return
	}
	if job.WorkspaceID != identity.WorkspaceID {
		apiError(w, mongo.ErrNoDocuments)
		return
	}
	if retry {
		err = h.worker.Retry(r.Context(), job.ID)
	} else {
		err = h.worker.Cancel(r.Context(), job.ID)
	}
	if err != nil {
		apiError(w, err)
		return
	}
	job, err = h.db.GetJob(r.Context(), job.ID)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, job)
}

func (h *APIHandler) QueueMetrics(w http.ResponseWriter, r *http.Request) {
	identity, err := h.identity(r, models.RoleReader)
	if err != nil {
		apiError(w, err)
		return
	}
	metrics, err := h.db.GetQueueMetrics(r.Context(), identity.WorkspaceID)
	if err != nil {
		apiError(w, err)
		return
	}
	jsonResponse(w, metrics)
}
