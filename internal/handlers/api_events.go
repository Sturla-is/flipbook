package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/metavert-io/flipbook/internal/models"
)

type apiWebhookEvent struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	WorkspaceID string    `json:"workspace_id"`
	ResourceID  string    `json:"resource_id,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
	Data        any       `json:"data"`
}

func (h *APIHandler) emitConversionReady(r *http.Request, fb *models.Flipbook) {
	if fb == nil || fb.Status != models.StatusReady || fb.CurrentRevisionID == "" {
		return
	}
	h.emitWebhook(r, fb.WorkspaceID, "conversion.ready", fb.ID, "conversion.ready:"+fb.CurrentRevisionID, map[string]any{
		"flipbook_id": fb.ID, "revision_id": fb.CurrentRevisionID, "page_count": fb.PageCount,
	})
}

func (h *APIHandler) emitWebhook(r *http.Request, workspaceID, eventType, resourceID, eventID string, data any) {
	if h == nil || h.db == nil || workspaceID == "" || eventType == "" {
		return
	}
	if eventID == "" {
		eventID = uuid.NewString()
	}
	event := apiWebhookEvent{
		ID: eventID, Type: eventType, WorkspaceID: workspaceID,
		ResourceID: resourceID, OccurredAt: h.now().UTC(), Data: data,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("API webhook %s payload failed: %v", eventType, err)
		return
	}
	subscriptions, err := h.db.ListWebhookSubscriptions(r.Context(), workspaceID, eventType, true)
	if err != nil {
		log.Printf("API webhook %s subscriptions failed: %v", eventType, err)
		return
	}
	for _, subscription := range subscriptions {
		delivery := &models.WebhookDelivery{
			WorkspaceID: workspaceID, SubscriptionID: subscription.ID,
			EventID: eventID, EventType: eventType, PayloadJSON: string(payload),
			IdempotencyKey: eventID, NextAttemptAt: h.now().UTC(),
		}
		if err := h.db.EnqueueWebhookDelivery(r.Context(), delivery); err != nil {
			log.Printf("API webhook %s enqueue for %s failed: %v", eventType, subscription.ID, err)
		}
	}
}
