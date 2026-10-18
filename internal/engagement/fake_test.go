package engagement

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type fakeStore struct {
	mu          sync.Mutex
	flipbooks   map[string]*models.Flipbook
	workspaces  map[string]*models.Workspace
	leads       map[string]*models.Lead
	subscribers map[string]*models.Subscriber
	webhooks    map[string]*models.WebhookSubscription
	deliveries  map[string]*models.WebhookDelivery
	settings    map[string]string
	sequence    int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		flipbooks: make(map[string]*models.Flipbook), workspaces: make(map[string]*models.Workspace),
		leads: make(map[string]*models.Lead), subscribers: make(map[string]*models.Subscriber),
		webhooks: make(map[string]*models.WebhookSubscription), deliveries: make(map[string]*models.WebhookDelivery),
		settings: make(map[string]string),
	}
}

func (f *fakeStore) nextID(prefix string) string {
	f.sequence++
	return fmt.Sprintf("%s-%d", prefix, f.sequence)
}

func (f *fakeStore) GetFlipbookContext(_ context.Context, id string) (*models.Flipbook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fb := f.flipbooks[id]; fb != nil {
		return fb, nil
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeStore) GetFlipbookInWorkspace(_ context.Context, workspaceID, id string) (*models.Flipbook, error) {
	fb, err := f.GetFlipbookContext(context.Background(), id)
	if err != nil || fb.WorkspaceID != workspaceID {
		return nil, mongo.ErrNoDocuments
	}
	return fb, nil
}

func (f *fakeStore) UpdateFlipbookMetadata(_ context.Context, fb *models.Flipbook) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fb == nil || f.flipbooks[fb.ID] == nil {
		return mongo.ErrNoDocuments
	}
	f.flipbooks[fb.ID] = fb
	return nil
}

func (f *fakeStore) SearchFlipbooks(_ context.Context, filter database.FlipbookFilter) ([]*models.Flipbook, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]*models.Flipbook, 0)
	for _, fb := range f.flipbooks {
		if filter.WorkspaceID == "" || fb.WorkspaceID == filter.WorkspaceID {
			items = append(items, fb)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Title < items[j].Title })
	return items, int64(len(items)), nil
}

func (f *fakeStore) GetWorkspace(_ context.Context, workspaceID string) (*models.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if workspace := f.workspaces[workspaceID]; workspace != nil {
		return workspace, nil
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeStore) UpsertLead(_ context.Context, lead *models.Lead) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	lead.Normalize()
	for id, existing := range f.leads {
		if existing.WorkspaceID == lead.WorkspaceID && existing.FlipbookID == lead.FlipbookID && existing.NormalizedEmail == lead.NormalizedEmail {
			lead.ID = id
			lead.CreatedAt = existing.CreatedAt
			f.leads[id] = lead
			return nil
		}
	}
	if lead.ID == "" {
		lead.ID = f.nextID("lead")
	}
	if lead.CreatedAt.IsZero() {
		lead.CreatedAt = time.Now().UTC()
	}
	f.leads[lead.ID] = lead
	return nil
}

func (f *fakeStore) ListLeads(_ context.Context, filter database.LeadFilter) ([]*models.Lead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]*models.Lead, 0)
	for _, lead := range f.leads {
		if lead.WorkspaceID != filter.WorkspaceID || filter.FlipbookID != "" && lead.FlipbookID != filter.FlipbookID {
			continue
		}
		if !filter.From.IsZero() && lead.CreatedAt.Before(filter.From) || !filter.To.IsZero() && !lead.CreatedAt.Before(filter.To) {
			continue
		}
		items = append(items, lead)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	limit := filter.Limit
	if limit == 0 {
		limit = 1000
	}
	if filter.Offset >= int64(len(items)) {
		return []*models.Lead{}, nil
	}
	items = items[filter.Offset:]
	if int64(len(items)) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (f *fakeStore) CountLeads(_ context.Context, workspaceID, flipbookID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, lead := range f.leads {
		if lead.WorkspaceID == workspaceID && (flipbookID == "" || lead.FlipbookID == flipbookID) {
			count++
		}
	}
	return count, nil
}

func (f *fakeStore) DeleteLead(_ context.Context, workspaceID, leadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if lead := f.leads[leadID]; lead != nil && lead.WorkspaceID == workspaceID {
		delete(f.leads, leadID)
	}
	return nil
}

func (f *fakeStore) DeleteLeadsForFlipbook(_ context.Context, workspaceID, flipbookID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, lead := range f.leads {
		if lead.WorkspaceID == workspaceID && lead.FlipbookID == flipbookID {
			delete(f.leads, id)
		}
	}
	return nil
}

func subscriberKey(workspaceID, email string) string {
	return workspaceID + "\n" + strings.ToLower(strings.TrimSpace(email))
}

func (f *fakeStore) UpsertSubscriber(_ context.Context, subscriber *models.Subscriber) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	subscriber.Normalize()
	key := subscriberKey(subscriber.WorkspaceID, subscriber.Email)
	if existing := f.subscribers[key]; existing != nil {
		subscriber.ID = existing.ID
		subscriber.CreatedAt = existing.CreatedAt
		if existing.Status != models.SubscriberUnsubscribed {
			subscriber.Status = existing.Status
			subscriber.ConfirmedAt = existing.ConfirmedAt
		}
	}
	if subscriber.ID == "" {
		subscriber.ID = f.nextID("subscriber")
	}
	f.subscribers[key] = subscriber
	return nil
}

func (f *fakeStore) GetSubscriber(_ context.Context, workspaceID, email string) (*models.Subscriber, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if subscriber := f.subscribers[subscriberKey(workspaceID, email)]; subscriber != nil {
		return subscriber, nil
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeStore) ConfirmSubscriber(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, subscriber := range f.subscribers {
		if subscriber.ConfirmationTokenHash == models.HashToken(token) && subscriber.Status == models.SubscriberPending && subscriber.ConfirmationExpiresAt != nil {
			now := time.Now().UTC()
			subscriber.Status = models.SubscriberSubscribed
			subscriber.ConfirmedAt = &now
			subscriber.ConfirmationTokenHash = ""
			subscriber.ConfirmationExpiresAt = nil
			return nil
		}
	}
	return mongo.ErrNoDocuments
}

func (f *fakeStore) UnsubscribeSubscriber(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, subscriber := range f.subscribers {
		if subscriber.UnsubscribeTokenHash == models.HashToken(token) {
			now := time.Now().UTC()
			subscriber.Status = models.SubscriberUnsubscribed
			subscriber.UnsubscribedAt = &now
			return nil
		}
	}
	return mongo.ErrNoDocuments
}

func (f *fakeStore) ListSubscribers(_ context.Context, workspaceID string, status models.SubscriberStatus) ([]*models.Subscriber, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]*models.Subscriber, 0)
	for _, subscriber := range f.subscribers {
		if subscriber.WorkspaceID == workspaceID && (status == "" || subscriber.Status == status) {
			items = append(items, subscriber)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (f *fakeStore) DeleteSubscriber(_ context.Context, workspaceID, subscriberID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, subscriber := range f.subscribers {
		if subscriber.ID == subscriberID && subscriber.WorkspaceID == workspaceID {
			delete(f.subscribers, key)
		}
	}
	return nil
}

func (f *fakeStore) CreateWebhookSubscription(_ context.Context, subscription *models.WebhookSubscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if subscription.ID == "" {
		subscription.ID = f.nextID("webhook")
	}
	f.webhooks[subscription.ID] = subscription
	return nil
}

func (f *fakeStore) ListWebhookSubscriptions(_ context.Context, workspaceID, eventType string, enabledOnly bool) ([]*models.WebhookSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]*models.WebhookSubscription, 0)
	for _, subscription := range f.webhooks {
		if subscription.WorkspaceID != workspaceID || enabledOnly && !subscription.Enabled {
			continue
		}
		matches := eventType == ""
		for _, event := range subscription.Events {
			if event == "*" || event == eventType {
				matches = true
			}
		}
		if matches {
			items = append(items, subscription)
		}
	}
	return items, nil
}

func (f *fakeStore) SetWebhookSubscriptionEnabled(_ context.Context, workspaceID, subscriptionID string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if subscription := f.webhooks[subscriptionID]; subscription != nil && subscription.WorkspaceID == workspaceID {
		subscription.Enabled = enabled
		return nil
	}
	return mongo.ErrNoDocuments
}

func (f *fakeStore) EnqueueWebhookDelivery(_ context.Context, delivery *models.WebhookDelivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if delivery.ID == "" {
		delivery.ID = f.nextID("delivery")
	}
	f.deliveries[delivery.ID] = delivery
	return nil
}

func (f *fakeStore) ListWebhookDeliveries(_ context.Context, workspaceID, subscriptionID string, limit int64) ([]*models.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]*models.WebhookDelivery, 0)
	for _, delivery := range f.deliveries {
		if delivery.WorkspaceID == workspaceID && (subscriptionID == "" || delivery.SubscriptionID == subscriptionID) {
			items = append(items, delivery)
		}
	}
	if limit > 0 && int64(len(items)) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (f *fakeStore) GetSetting(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.settings[key]; ok {
		return value, nil
	}
	return "", mongo.ErrNoDocuments
}

func (f *fakeStore) SetSetting(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[key] = value
	return nil
}

type fakePublisher struct {
	flipbook  *models.Flipbook
	captured  bool
	dismissed bool
	access    publishing.AccessRequest
	calls     int
}

func (f *fakePublisher) IssueLeadGrant(_ context.Context, _, _ string, captured, dismissed bool, request publishing.AccessRequest) (*publishing.GrantResult, error) {
	f.calls++
	f.captured, f.dismissed, f.access = captured, dismissed, request
	return &publishing.GrantResult{
		Token: "signed-grant", ExpiresAt: time.Now().Add(time.Hour),
		Cookie: &http.Cookie{Name: "access", Value: "signed-grant", HttpOnly: true},
	}, nil
}

func (f *fakePublisher) AuthorizeViewer(_ context.Context, _, _ string, _ publishing.AccessRequest) (*publishing.Authorization, error) {
	return &publishing.Authorization{Flipbook: *f.flipbook, Decision: publishing.Decision{Allowed: true}}, nil
}

func (f *fakePublisher) GrantFromRequest(_ *http.Request, _ string) string { return "existing-grant" }

type fakeMailer struct {
	enabled  bool
	messages []Message
	err      error
}

func (f *fakeMailer) Enabled() bool { return f.enabled }

func (f *fakeMailer) Send(_ context.Context, message Message) error {
	f.messages = append(f.messages, message)
	return f.err
}
