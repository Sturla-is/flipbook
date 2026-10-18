package analytics

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
)

type EventLister interface {
	ListAnalyticsEvents(ctx context.Context, filter models.AnalyticsFilter, limit int64) ([]*models.AnalyticsEvent, error)
}

// LoadEvents uses recursive date splitting so a full page of results is never
// mistaken for the end of the dataset. The database method remains the sole
// query API; no additional collection or index is required.
func LoadEvents(ctx context.Context, store EventLister, filter models.AnalyticsFilter) ([]*models.AnalyticsEvent, error) {
	if filter.From.IsZero() || filter.To.IsZero() || !filter.From.Before(filter.To) {
		return nil, fmt.Errorf("a bounded analytics date range is required")
	}
	total := 0
	events, err := loadEventRange(ctx, store, filter, &total)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].OccurredAt.Before(events[j].OccurredAt)
	})
	return events, nil
}

func loadEventRange(ctx context.Context, store EventLister, filter models.AnalyticsFilter, total *int) ([]*models.AnalyticsEvent, error) {
	const limit = int64(10000)
	events, err := store.ListAnalyticsEvents(ctx, filter, limit)
	if err != nil {
		return nil, err
	}
	if int64(len(events)) < limit {
		*total += len(events)
		if *total > MaxReportEvents {
			return nil, ErrResultTooLarge
		}
		return events, nil
	}
	if filter.To.Sub(filter.From) <= time.Millisecond {
		return nil, ErrResultTooLarge
	}
	midpoint := filter.From.Add(filter.To.Sub(filter.From) / 2)
	if !midpoint.After(filter.From) || !midpoint.Before(filter.To) {
		return nil, ErrResultTooLarge
	}
	left := filter
	left.To = midpoint
	right := filter
	right.From = midpoint
	leftEvents, err := loadEventRange(ctx, store, left, total)
	if err != nil {
		return nil, err
	}
	rightEvents, err := loadEventRange(ctx, store, right, total)
	if err != nil {
		return nil, err
	}
	return append(leftEvents, rightEvents...), nil
}

func SanitizeEvent(event *models.AnalyticsEvent) PublicEvent {
	if event == nil {
		return PublicEvent{}
	}
	eventType, ok := NormalizeEventType(string(event.Type))
	if !ok {
		eventType = event.Type
	}
	result := PublicEvent{
		ID: event.ID, WorkspaceID: event.WorkspaceID, FlipbookID: event.FlipbookID,
		RevisionID: event.RevisionID, Type: eventType, OccurredAt: event.OccurredAt.UTC(),
		Page: event.Page, ActiveSeconds: event.ActiveSeconds, Value: event.Value,
		Referrer: models.NormalizeReferrer(event.Referrer), UTM: event.UTM,
		Placement: event.Placement, Device: event.Device, Browser: event.Browser,
		Country: event.Country, SearchTerms: event.SearchTerms, TargetHost: event.TargetHost,
	}
	for _, key := range []string{"cta_id", "link_id", "channel", "method", "format", "embed_host"} {
		if value := event.Metadata[key]; value != "" {
			if result.Metadata == nil {
				result.Metadata = make(map[string]string)
			}
			result.Metadata[key] = value
		}
	}
	return result
}

func SanitizeEvents(events []*models.AnalyticsEvent) []PublicEvent {
	result := make([]PublicEvent, 0, len(events))
	for _, event := range events {
		result = append(result, SanitizeEvent(event))
	}
	return result
}
