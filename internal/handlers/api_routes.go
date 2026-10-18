package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/models"
)

// RegisterRoutes mounts the complete versioned API. Existing main.go can keep
// its legacy /api/flipbooks registrations while migrating to this method.
func (h *APIHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1", h.RegisterV1Routes)
}

// RegisterV1Routes registers routes relative to an /api/v1 mount. Each route
// has its own API-key scope middleware; session principals must be populated by
// the auth package's middleware before this router is called.
func (h *APIHandler) RegisterV1Routes(r chi.Router) {
	read := h.RequireScope(models.ScopeFlipbooksRead)
	write := h.RequireScope(models.ScopeFlipbooksWrite)
	analyticsRead := h.RequireScope(models.ScopeAnalyticsRead)
	leadsRead := h.RequireScope(models.ScopeLeadsRead)
	leadsWrite := h.RequireScope(models.ScopeLeadsWrite)
	salesRead := h.RequireScope(models.ScopeSalesRead)
	workspaceAdmin := h.RequireScope(models.ScopeWorkspaceAdmin)
	integrationsWrite := h.RequireScope(models.ScopeIntegrationsWrite)

	r.With(read).Get("/flipbooks", h.ListFlipbooksV1)
	r.With(write).Post("/flipbooks", h.UploadFlipbook)
	r.With(write).Post("/flipbooks/import", h.ImportURL)
	r.With(write).Post("/flipbooks/bulk", h.BulkFlipbooks)
	r.With(read).Get("/flipbooks/{id}", h.GetFlipbook)
	r.With(write).Patch("/flipbooks/{id}", h.PatchFlipbook)
	r.With(write).Delete("/flipbooks/{id}", h.DeleteFlipbook)
	r.With(read).Get("/flipbooks/{id}/status", h.FlipbookStatus)
	r.With(write).Post("/flipbooks/{id}/publish", h.PublishFlipbook)
	r.With(write).Post("/flipbooks/{id}/unpublish", h.UnpublishFlipbook)
	r.With(write).Post("/flipbooks/{id}/submit-review", h.SubmitFlipbookReview)
	r.With(write).Post("/flipbooks/{id}/review/approve", h.ApproveFlipbookReview)
	r.With(write).Post("/flipbooks/{id}/review/reject", h.RejectFlipbookReview)
	r.With(write).Post("/flipbooks/{id}/archive", h.ArchiveFlipbook)
	r.With(write).Post("/flipbooks/{id}/restore", h.RestoreFlipbook)
	r.With(write).Post("/flipbooks/{id}/purge", h.PurgeFlipbook)
	r.With(write).Post("/flipbooks/{id}/duplicate", h.DuplicateFlipbook)
	r.With(write).Post("/flipbooks/{id}/replace", h.ReplaceFlipbook)
	r.With(write).Post("/flipbooks/{id}/retry", h.RetryFlipbook)
	r.With(write).Post("/flipbooks/{id}/cancel", h.CancelFlipbook)
	r.With(write).Put("/flipbooks/{id}/tags", h.UpdateFlipbookTags)
	r.With(write).Post("/flipbooks/{id}/tags", h.UpdateFlipbookTags)
	r.With(write).Delete("/flipbooks/{id}/tags/{tag}", h.DeleteFlipbookTag)
	r.With(read).Get("/flipbooks/{id}/revisions", h.ListRevisions)
	r.With(read).Get("/flipbooks/{id}/revisions/{revisionID}", h.GetRevision)
	r.With(write).Delete("/flipbooks/{id}/revisions/{revisionID}", h.DeleteRevision)
	r.With(write).Post("/flipbooks/{id}/revisions/{revisionID}/activate", h.ActivateRevision)
	r.With(write).Post("/flipbooks/{id}/revisions/{revisionID}/rollback", h.RollbackRevision)
	r.With(write).Post("/flipbooks/{id}/rollback", h.RollbackRevision)
	r.With(read).Get("/flipbooks/{id}/links", h.ListLinkOverlays)
	r.With(write).Post("/flipbooks/{id}/links", h.CreateLinkOverlay)
	r.With(write).Patch("/flipbooks/{id}/links/{linkID}", h.PatchLinkOverlay)
	r.With(write).Delete("/flipbooks/{id}/links/{linkID}", h.DeleteLinkOverlay)

	r.With(read).Get("/collections", h.ListCollections)
	r.With(write).Post("/collections", h.CreateCollection)
	r.With(read).Get("/collections/{collectionID}", h.GetCollection)
	r.With(write).Patch("/collections/{collectionID}", h.PatchCollection)
	r.With(write).Delete("/collections/{collectionID}", h.DeleteCollection)
	r.With(write).Post("/collections/{collectionID}/flipbooks/{id}", h.AddFlipbookToCollection)
	r.With(write).Delete("/collections/{collectionID}/flipbooks/{id}", h.RemoveFlipbookFromCollection)
	r.With(read).Get("/tags", h.ListTags)

	r.With(read).Get("/jobs", h.ListJobs)
	r.With(read).Get("/jobs/metrics", h.QueueMetrics)
	r.With(read).Get("/jobs/{jobID}", h.GetJob)
	r.With(write).Post("/jobs/{jobID}/retry", h.RetryJob)
	r.With(write).Post("/jobs/{jobID}/cancel", h.CancelJob)

	r.With(analyticsRead).Get("/analytics", h.AnalyticsSummary)
	r.With(analyticsRead).Get("/analytics/summary", h.AnalyticsSummary)
	r.With(analyticsRead).Get("/analytics/breakdown", h.AnalyticsBreakdown)
	r.With(analyticsRead).Get("/analytics/pages", h.AnalyticsPages)
	r.With(analyticsRead).Get("/analytics/events", h.AnalyticsEvents)
	r.With(analyticsRead).Get("/analytics/export.csv", h.ExportAnalyticsCSV)

	r.With(leadsRead).Get("/leads", h.ListLeads)
	r.With(leadsRead).Get("/leads/export.csv", h.ExportLeadsCSV)
	r.With(leadsWrite).Delete("/leads/{leadID}", h.DeleteLead)
	r.With(leadsRead).Get("/subscribers", h.ListSubscribers)
	r.With(leadsWrite).Delete("/subscribers/{subscriberID}", h.DeleteSubscriber)

	r.With(salesRead).Get("/sales", h.ListPurchases)
	r.With(salesRead).Get("/sales/summary", h.SalesSummary)
	r.With(salesRead).Get("/sales/export.csv", h.ExportSalesCSV)
	r.With(salesRead).Get("/sales/{purchaseID}", h.GetPurchase)
	r.With(salesRead).Post("/sales/{purchaseID}/entitlement/revoke", h.RevokePurchaseEntitlement)
	r.With(salesRead).Post("/sales/{purchaseID}/entitlement/restore", h.RestorePurchaseEntitlement)

	r.With(read).Get("/workspaces", h.ListWorkspaces)
	r.With(workspaceAdmin).Post("/workspaces", h.CreateWorkspace)
	r.With(read).Get("/workspaces/{workspaceID}", h.GetWorkspace)
	r.With(workspaceAdmin).Patch("/workspaces/{workspaceID}", h.PatchWorkspace)
	r.With(workspaceAdmin).Get("/workspaces/{workspaceID}/members", h.ListMemberships)
	r.With(workspaceAdmin).Put("/workspaces/{workspaceID}/members/{userID}", h.PutMembership)
	r.With(workspaceAdmin).Delete("/workspaces/{workspaceID}/members/{userID}", h.RemoveMembership)
	r.With(workspaceAdmin).Post("/workspaces/{workspaceID}/invitations", h.CreateInvitation)
	r.With(workspaceAdmin).Delete("/workspaces/{workspaceID}/invitations/{invitationID}", h.RevokeInvitation)
	r.With(workspaceAdmin).Get("/workspaces/{workspaceID}/api-keys", h.ListAPIKeys)
	r.With(workspaceAdmin).Post("/workspaces/{workspaceID}/api-keys", h.CreateAPIKey)
	r.With(workspaceAdmin).Delete("/workspaces/{workspaceID}/api-keys/{keyID}", h.RevokeAPIKey)
	r.With(workspaceAdmin).Get("/workspaces/{workspaceID}/audit", h.ListAuditEvents)
	r.With(workspaceAdmin).Post("/workspaces/{workspaceID}/maintenance/purge", h.PurgeDueFlipbooks)

	r.With(integrationsWrite).Get("/workspaces/{workspaceID}/integrations", h.ListIntegrations)
	r.With(integrationsWrite).Delete("/workspaces/{workspaceID}/integrations/{connectionID}", h.DisconnectIntegration)
	r.With(integrationsWrite).Get("/workspaces/{workspaceID}/webhooks", h.ListWebhooks)
	r.With(integrationsWrite).Post("/workspaces/{workspaceID}/webhooks", h.CreateWebhook)
	r.With(integrationsWrite).Patch("/workspaces/{workspaceID}/webhooks/{webhookID}", h.SetWebhookEnabled)
	r.With(integrationsWrite).Delete("/workspaces/{workspaceID}/webhooks/{webhookID}", h.DisableWebhook)
	r.With(integrationsWrite).Get("/workspaces/{workspaceID}/webhooks/{webhookID}/deliveries", h.ListWebhookDeliveries)
	r.With(integrationsWrite).Post("/workspaces/{workspaceID}/webhooks/{webhookID}/test", h.TestWebhook)

	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	})
}
