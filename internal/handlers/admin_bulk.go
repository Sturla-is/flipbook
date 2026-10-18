package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
	"github.com/jonradoff/flipbook/internal/worker"
)

type adminBulkResult struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (h *AdminHandler) Bulk(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleReader)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, adminInputf("The bulk-action form is invalid."))
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	if action == "delete" {
		action = "trash"
	}
	required := models.RoleEditor
	switch action {
	case "publish", "unpublish", "archive", "restore":
		required = models.RolePublisher
	case "trash":
		required = models.RoleAdmin
	case "retry", "add_tags", "remove_tags", "add_collection", "remove_collection", "set_collection":
	default:
		h.renderError(w, r, adminInputf("Choose a supported bulk action."))
		return
	}
	if !actor.Role.AtLeast(required) {
		h.renderError(w, r, errAdminForbidden)
		return
	}
	ids := uniqueAdminStrings(append(cloneAdminStrings(r.Form["ids"]), r.Form["flipbook_ids"]...))
	if len(ids) == 0 || len(ids) > 100 {
		h.renderError(w, r, adminInputf("Select between 1 and 100 publications for a bulk action."))
		return
	}
	tags, err := normalizedAdminTags(r.FormValue("tags"))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if (action == "add_tags" || action == "remove_tags") && len(tags) == 0 {
		h.renderError(w, r, adminInputf("Enter at least one tag for this bulk action."))
		return
	}
	collectionID := strings.TrimSpace(r.FormValue("collection_id"))
	if (action == "add_collection" || action == "remove_collection") && collectionID == "" {
		h.renderError(w, r, adminInputf("Choose a collection for this bulk action."))
		return
	}
	if collectionID != "" {
		if _, err := h.db.GetCollection(r.Context(), actor.WorkspaceID, collectionID); err != nil {
			h.renderError(w, r, adminInputf("The selected collection is invalid."))
			return
		}
	}
	results := make([]adminBulkResult, 0, len(ids))
	succeeded := 0
	for _, id := range ids {
		result := adminBulkResult{ID: id}
		flipbook, getErr := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, id)
		if getErr != nil {
			_, result.Message = adminErrorMessage(getErr)
			results = append(results, result)
			continue
		}
		result.Title = flipbook.Title
		err := h.applyBulkAction(r, actor, flipbook, action, tags, collectionID)
		if err != nil {
			_, result.Message = adminErrorMessage(err)
		} else {
			result.OK = true
			result.Message = "Completed"
			succeeded++
		}
		results = append(results, result)
	}
	failed := len(ids) - succeeded
	h.audit(r, actor, "publication.bulk."+action, "flipbook", "", map[bool]string{true: "success", false: "partial"}[failed == 0], map[string]string{
		"requested": strconv.Itoa(len(ids)), "succeeded": strconv.Itoa(succeeded), "failed": strconv.Itoa(failed),
	})
	if adminWantsJSON(r) {
		adminJSONResponse(w, http.StatusOK, map[string]any{"action": action, "succeeded": succeeded, "failed": failed, "results": results})
		return
	}
	returnTo := safeAdminReturn(r.FormValue("return_to"), "/admin")
	parsed, _ := url.Parse(returnTo)
	query := parsed.Query()
	query.Set("notice", fmt.Sprintf("Bulk %s: %d succeeded, %d failed.", action, succeeded, failed))
	if failed > 0 {
		firstMessage := ""
		for _, result := range results {
			if !result.OK {
				firstMessage = result.Message
				break
			}
		}
		query.Set("error", fmt.Sprintf("%d item(s) were not changed. First error: %s", failed, firstMessage))
	}
	parsed.RawQuery = query.Encode()
	http.Redirect(w, r, parsed.String(), http.StatusSeeOther)
}

func (h *AdminHandler) applyBulkAction(r *http.Request, actor adminActor, flipbook *models.Flipbook, action string, tags []string, collectionID string) error {
	ctx := r.Context()
	switch action {
	case "publish":
		if h.publisher == nil {
			return errAdminUnavailable
		}
		_, err := h.publisher.Publish(ctx, publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, flipbook.ID, publishing.PublishOptions{Visibility: flipbook.Visibility})
		return err
	case "unpublish":
		if h.publisher == nil {
			return errAdminUnavailable
		}
		_, err := h.publisher.Unpublish(ctx, publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, flipbook.ID)
		return err
	case "archive":
		if h.publisher == nil {
			return errAdminUnavailable
		}
		return h.publisher.Archive(ctx, publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, flipbook.ID, "Bulk archive")
	case "restore":
		if h.publisher == nil {
			return errAdminUnavailable
		}
		_, err := h.publisher.Restore(ctx, publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, flipbook.ID)
		return err
	case "trash":
		if h.publisher != nil {
			_, err := h.publisher.SchedulePurge(ctx, publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, flipbook.ID)
			return err
		}
		return h.db.SoftDeleteFlipbook(ctx, actor.WorkspaceID, flipbook.ID, 30*24*time.Hour)
	case "retry":
		return h.queueBulkReconvert(r, actor, flipbook)
	case "add_tags", "remove_tags":
		flipbook.Tags = bulkTags(flipbook.Tags, tags, action == "add_tags")
		flipbook.ApplyDefaults()
		return h.db.UpdateFlipbookMetadata(ctx, flipbook)
	case "add_collection", "remove_collection", "set_collection":
		desired := cloneAdminStrings(flipbook.CollectionIDs)
		switch action {
		case "set_collection":
			desired = nil
			if collectionID != "" {
				desired = []string{collectionID}
			}
		case "add_collection":
			desired = uniqueAdminStrings(append(desired, collectionID))
		case "remove_collection":
			desired = removeAdminString(desired, collectionID)
		}
		return h.syncFlipbookCollections(r, actor.WorkspaceID, flipbook.ID, flipbook.CollectionIDs, desired)
	}
	return adminInputf("Unsupported bulk action.")
}

func (h *AdminHandler) queueBulkReconvert(r *http.Request, actor adminActor, flipbook *models.Flipbook) error {
	current, err := h.db.GetCurrentRevision(r.Context(), actor.WorkspaceID, flipbook.ID)
	if err != nil {
		return err
	}
	revision := current
	if flipbook.Status == models.StatusReady {
		revision = &models.Revision{
			WorkspaceID: actor.WorkspaceID, FlipbookID: flipbook.ID, SourceFilename: current.SourceFilename,
			SourceKey: current.SourceKey, OriginalGridFSFileID: current.OriginalGridFSFileID,
			SourceChecksum: current.SourceChecksum, ExternalSource: current.ExternalSource,
			ExternalID: current.ExternalID, CreatedBy: actor.UserID,
		}
		if err := h.db.CreateRevision(r.Context(), revision); err != nil {
			return err
		}
	}
	_, err = h.worker.EnqueueContext(r.Context(), worker.Job{
		FlipbookID: flipbook.ID, WorkspaceID: actor.WorkspaceID, RevisionID: revision.ID,
		SourcePath: h.sourceForRevision(flipbook.ID, revision),
	})
	return err
}

func bulkTags(current, changes []string, add bool) []string {
	if add {
		return uniqueAdminStrings(append(cloneAdminStrings(current), changes...))
	}
	for _, tag := range changes {
		current = removeAdminString(current, tag)
	}
	return current
}

func removeAdminString(values []string, remove string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != remove {
			result = append(result, value)
		}
	}
	return result
}
