package handlers

import (
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/publishing"
)

func (h *AdminHandler) lifecycleError(w http.ResponseWriter, r *http.Request, actor adminActor, id string, err error) {
	status, message := adminErrorMessage(err)
	if status >= http.StatusInternalServerError {
		log.Printf("Admin lifecycle action failed (flipbook %s): %v", id, err)
	}
	if adminWantsJSON(r) {
		adminJSONError(w, message, status)
		return
	}
	h.renderDetail(w, r, actor, id, status, map[string]any{"Error": message})
}

func (h *AdminHandler) mutationSuccess(w http.ResponseWriter, r *http.Request, id, message string) {
	if adminWantsJSON(r) {
		adminJSONResponse(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": message})
		return
	}
	target := "/admin/flipbooks/" + id
	if message != "" {
		target += "?notice=" + url.QueryEscape(message)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *AdminHandler) Publish(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RolePublisher)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.lifecycleError(w, r, actor, id, errAdminUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.lifecycleError(w, r, actor, id, adminInputf("The publication form is invalid."))
		return
	}
	flipbook, err := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	visibility := flipbook.Visibility
	if value := models.Visibility(r.FormValue("visibility")); value != "" {
		visibility = value
	}
	publishAt, err := parseAdminTime(r.FormValue("publish_at"), "Scheduled publish time")
	if err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	expiresAt, err := parseAdminTime(r.FormValue("expires_at"), "Publication expiry")
	if err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	updated, err := h.publisher.Publish(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, publishing.PublishOptions{
		Visibility: visibility, PublishAt: publishAt, ExpiresAt: expiresAt,
	})
	if err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	message := "Publication published."
	if updated.PublicationState == models.PublicationDraft && updated.PublishAt != nil && updated.PublishAt.After(h.now()) {
		message = "Publication scheduled for " + updated.PublishAt.UTC().Format(time.RFC3339) + "."
	}
	h.audit(r, actor, "publication.publish", "flipbook", id, "success", map[string]string{"visibility": string(updated.Visibility)})
	h.mutationSuccess(w, r, id, message)
}

func (h *AdminHandler) Unpublish(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RolePublisher)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.lifecycleError(w, r, actor, id, errAdminUnavailable)
		return
	}
	if _, err := h.publisher.Unpublish(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id); err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	h.audit(r, actor, "publication.unpublish", "flipbook", id, "success", nil)
	h.mutationSuccess(w, r, id, "Publication unpublished.")
}

func (h *AdminHandler) SubmitReview(w http.ResponseWriter, r *http.Request) {
	h.reviewMutation(w, r, models.RoleEditor, "submit")
}

func (h *AdminHandler) ApproveReview(w http.ResponseWriter, r *http.Request) {
	h.reviewMutation(w, r, models.RoleAdmin, "approve")
}

func (h *AdminHandler) RejectReview(w http.ResponseWriter, r *http.Request) {
	h.reviewMutation(w, r, models.RoleAdmin, "reject")
}

func (h *AdminHandler) reviewMutation(w http.ResponseWriter, r *http.Request, role models.Role, action string) {
	actor, err := adminActorFromRequest(r, role)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.lifecycleError(w, r, actor, id, errAdminUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.lifecycleError(w, r, actor, id, adminInputf("The review form is invalid."))
		return
	}
	note := r.FormValue("note")
	switch action {
	case "submit":
		_, err = h.publisher.SubmitForReview(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, note)
	case "approve":
		_, err = h.publisher.ApproveReview(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, note)
	case "reject":
		_, err = h.publisher.RejectReview(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, note)
	}
	if err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	messages := map[string]string{"submit": "Publication submitted for review.", "approve": "Review approved.", "reject": "Review rejected."}
	h.audit(r, actor, "publication.review."+action, "flipbook", id, "success", nil)
	h.mutationSuccess(w, r, id, messages[action])
}

func (h *AdminHandler) Archive(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RolePublisher)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.lifecycleError(w, r, actor, id, errAdminUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.lifecycleError(w, r, actor, id, adminInputf("The archive form is invalid."))
		return
	}
	if err := h.publisher.Archive(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, r.FormValue("reason")); err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	h.audit(r, actor, "publication.archive", "flipbook", id, "success", nil)
	h.mutationSuccess(w, r, id, "Publication archived. It is no longer publicly available.")
}

func (h *AdminHandler) Restore(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RolePublisher)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.lifecycleError(w, r, actor, id, errAdminUnavailable)
		return
	}
	if _, err := h.publisher.Restore(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id); err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	h.audit(r, actor, "publication.restore", "flipbook", id, "success", nil)
	h.mutationSuccess(w, r, id, "Publication restored from the archive.")
}

func (h *AdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	purgeAfter := h.now().Add(30 * 24 * time.Hour)
	if h.publisher != nil {
		purgeAfter, err = h.publisher.SchedulePurge(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id)
	} else {
		err = h.db.SoftDeleteFlipbook(r.Context(), actor.WorkspaceID, id, 30*24*time.Hour)
	}
	if err != nil {
		h.lifecycleError(w, r, actor, id, err)
		return
	}
	h.audit(r, actor, "publication.trash", "flipbook", id, "success", map[string]string{"purge_after": purgeAfter.UTC().Format(time.RFC3339)})
	if adminWantsJSON(r) {
		adminJSONResponse(w, http.StatusOK, map[string]any{"ok": true, "id": id, "purge_after": purgeAfter})
		return
	}
	http.Redirect(w, r, "/admin?notice="+url.QueryEscape("Publication moved to trash. Permanent purge is scheduled for "+purgeAfter.UTC().Format(time.RFC3339)+"."), http.StatusSeeOther)
}
