package handlers

import "github.com/go-chi/chi/v5"

func (h *AdminHandler) RegisterRoutes(r chi.Router) {
	r.Get("/admin", h.Index)
	r.Get("/admin/upload", h.UploadForm)
	r.Post("/admin/upload", h.Upload)
	r.Post("/admin/import", h.ImportURL)
	r.Post("/admin/bulk", h.Bulk)

	r.Get("/admin/collections", h.Collections)
	r.Post("/admin/collections/create", h.CreateCollection)
	r.Post("/admin/collections/{id}/update", h.UpdateCollection)
	r.Post("/admin/collections/{id}/delete", h.DeleteCollection)

	r.Get("/admin/team", h.Team)
	r.Post("/admin/team/invitations", h.InviteMember)
	r.Post("/admin/team/invitations/{id}/revoke", h.RevokeInvitation)
	r.Post("/admin/team/members/{userID}/role", h.UpdateMemberRole)
	r.Post("/admin/team/members/{userID}/remove", h.RemoveMember)
	r.Post("/admin/team/members/{userID}/disabled", h.SetMemberDisabled)
	r.Post("/admin/team/members/{userID}/sessions", h.RevokeMemberSessions)
	r.Post("/admin/team/members/{userID}/profile", h.UpdateMemberProfile)

	r.Get("/admin/workspace", h.Workspace)
	r.Post("/admin/workspace/settings", h.UpdateWorkspace)
	r.Post("/admin/workspace/create", h.CreateWorkspace)

	r.Get("/admin/security", h.Security)
	r.Post("/admin/security/api-keys", h.CreateAPIKey)
	r.Post("/admin/security/api-keys/{id}/revoke", h.RevokeAPIKey)

	r.Get("/admin/flipbooks/{id}", h.Detail)
	r.Get("/admin/flipbooks/{id}/status", h.ConversionStatus)
	r.Post("/admin/flipbooks/{id}/settings", h.Settings)
	r.Post("/admin/flipbooks/{id}/slug", h.UpdateSlug)
	r.Post("/admin/flipbooks/{id}/replace", h.Replace)
	r.Post("/admin/flipbooks/{id}/retry", h.Retry)
	r.Post("/admin/flipbooks/{id}/reconvert", h.Reconvert)
	r.Post("/admin/flipbooks/{id}/cancel", h.Cancel)
	r.Post("/admin/flipbooks/{id}/duplicate", h.Duplicate)
	r.Post("/admin/flipbooks/{id}/publish", h.Publish)
	r.Post("/admin/flipbooks/{id}/unpublish", h.Unpublish)
	r.Post("/admin/flipbooks/{id}/review/submit", h.SubmitReview)
	r.Post("/admin/flipbooks/{id}/review/approve", h.ApproveReview)
	r.Post("/admin/flipbooks/{id}/review/reject", h.RejectReview)
	r.Post("/admin/flipbooks/{id}/archive", h.Archive)
	r.Post("/admin/flipbooks/{id}/restore", h.Restore)
	r.Post("/admin/flipbooks/{id}/delete", h.Delete)
	r.Post("/admin/flipbooks/{id}/jobs/{jobID}/cancel", h.CancelJob)
	r.Post("/admin/flipbooks/{id}/jobs/{jobID}/retry", h.RetryJob)
	r.Post("/admin/flipbooks/{id}/revisions/{revisionID}/rollback", h.Rollback)
	r.Post("/admin/flipbooks/{id}/revisions/{revisionID}/activate", h.ActivateRevision)
	r.Post("/admin/flipbooks/{id}/share-tokens", h.CreateShareToken)
	r.Post("/admin/flipbooks/{id}/share-tokens/{tokenID}/revoke", h.RevokeShareToken)
}
