package analytics

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/metavert-io/flipbook/internal/models"
)

var (
	eventIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	labelPattern   = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._:/@+-]{0,99}$`)
)

// EnrichOptions identifies the publication and server-controlled event context.
type EnrichOptions struct {
	Options
	WorkspaceID string
	FlipbookID  string
	RevisionID  string
	PageCount   int
	Query       url.Values
	Now         time.Time
}

func NormalizeEventType(value string) (models.AnalyticsEventType, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "view":
		return models.AnalyticsView, true
	case "qualified_read":
		return models.AnalyticsQualifiedRead, true
	case "page_view", "page_exposure":
		return models.AnalyticsPageExposure, true
	case "active_time":
		return models.AnalyticsActiveTime, true
	case "furthest_page":
		return models.AnalyticsFurthestPage, true
	case "completion":
		return models.AnalyticsCompletion, true
	case "cta_click":
		return models.AnalyticsCTAClick, true
	case "outbound_click":
		return models.AnalyticsOutboundClick, true
	case "download":
		return models.AnalyticsDownload, true
	case "search":
		return models.AnalyticsSearch, true
	case "share":
		return models.AnalyticsShare, true
	default:
		return "", false
	}
}

// BuildEvent validates and enriches one public event. No raw session/reader ID,
// raw IP, full outbound URL, referrer query, or unlisted metadata is returned.
func BuildEvent(input IncomingEvent, request RequestContext, options EnrichOptions) (*models.AnalyticsEvent, error) {
	eventType, ok := NormalizeEventType(input.Type)
	if !ok {
		return nil, fmt.Errorf("%w: unsupported public type %q", ErrInvalidEvent, input.Type)
	}
	if options.WorkspaceID == "" || options.FlipbookID == "" {
		return nil, fmt.Errorf("%w: publication context is required", ErrInvalidEvent)
	}
	if input.DurationMS < 0 || input.DurationMS > MaxDurationMS {
		return nil, fmt.Errorf("%w: duration_ms must be between 0 and %d", ErrInvalidEvent, MaxDurationMS)
	}
	if input.Page < 0 || (options.PageCount > 0 && input.Page > options.PageCount) {
		return nil, fmt.Errorf("%w: page is outside the publication", ErrInvalidEvent)
	}
	if (eventType == models.AnalyticsPageExposure || eventType == models.AnalyticsFurthestPage) && input.Page < 1 {
		return nil, fmt.Errorf("%w: page is required for %s", ErrInvalidEvent, input.Type)
	}
	if input.SessionID != "" && !ValidIdentifier(input.SessionID) {
		return nil, fmt.Errorf("%w: session_id must contain 8-200 safe characters", ErrInvalidEvent)
	}

	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	opts := options.Options.normalized()
	event := &models.AnalyticsEvent{
		WorkspaceID: options.WorkspaceID,
		FlipbookID:  options.FlipbookID,
		RevisionID:  cleanLabel(options.RevisionID, 200),
		Type:        eventType,
		OccurredAt:  now,
		Page:        input.Page,
		Referrer:    models.NormalizeReferrer(request.Referrer),
		UTM:         campaign(input.Metadata, options.Query, request.Referrer),
		Placement:   placement(input.Metadata),
		Country:     cleanCountry(request.Country, opts.AllowCountry),
		Metadata:    make(map[string]string),
	}
	event.Device, event.Browser = models.CoarseDeviceBrowser(request.UserAgent)
	if eventType == models.AnalyticsCompletion && event.Page == 0 {
		event.Page = options.PageCount
	}
	if eventType == models.AnalyticsActiveTime || eventType == models.AnalyticsPageExposure {
		event.ActiveSeconds = input.DurationMS / 1000
		if input.DurationMS > 0 && event.ActiveSeconds == 0 {
			event.ActiveSeconds = 1
		}
	}
	if eventType == models.AnalyticsFurthestPage {
		event.Value = int64(input.Page)
	}
	if IdentifiersAllowed(opts, request) {
		sessionID := input.SessionID
		if sessionID == "" {
			sessionID = request.SessionID
		}
		if ValidIdentifier(sessionID) {
			event.SessionIDHash = IdentifierHash(opts.ReaderIDPepper, options.WorkspaceID, "session", sessionID)
		}
		if ValidIdentifier(request.ReaderID) {
			event.ReaderIDHash = IdentifierHash(opts.ReaderIDPepper, options.WorkspaceID, "reader", request.ReaderID)
		}
	}

	for _, key := range []string{"cta_id", "link_id", "channel", "method", "format"} {
		value := firstMetadata(input.Metadata, key)
		if key == "cta_id" && value == "" {
			value = firstMetadata(input.Metadata, "cta")
		}
		if value != "" && labelPattern.MatchString(value) {
			event.Metadata[key] = value
		}
	}
	if host := cleanHost(firstMetadata(input.Metadata, "embed_host")); host != "" {
		event.Metadata["embed_host"] = host
	}
	if eventType == models.AnalyticsSearch {
		event.SearchTerms = cleanSearch(firstMetadata(input.Metadata, "query", "search_terms", "search"))
	}
	if eventType == models.AnalyticsCTAClick || eventType == models.AnalyticsOutboundClick {
		event.TargetHost = targetHost(firstMetadata(input.Metadata, "target_url", "url"))
	}
	if rawID := strings.TrimSpace(firstMetadata(input.Metadata, "event_id")); rawID != "" {
		if !eventIDPattern.MatchString(rawID) {
			return nil, fmt.Errorf("%w: metadata.event_id has an invalid format", ErrInvalidEvent)
		}
		event.ID = options.FlipbookID + ":" + rawID
	}
	if len(event.Metadata) == 0 {
		event.Metadata = nil
	}
	event.PrepareForStorage(opts.Retention, opts.ConsentPolicy, request.Consented)
	return event, nil
}

func firstMetadata(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func cleanLabel(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if utf8.RuneCountInString(value) > maxRunes {
		runes := []rune(value)
		value = string(runes[:maxRunes])
	}
	return value
}

func cleanSearch(value string) string {
	return strings.ToLower(cleanLabel(value, 200))
}

func cleanCountry(value string, enabled bool) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !enabled || !countryPattern.MatchString(value) || value == "XX" || value == "ZZ" || value == "T1" {
		return ""
	}
	return value
}

func placement(metadata map[string]string) string {
	if strings.EqualFold(firstMetadata(metadata, "placement"), PlacementEmbed) {
		return PlacementEmbed
	}
	return PlacementStandalone
}

func targetHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return cleanHost(parsed.Hostname())
}

func cleanHost(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || len(raw) > 253 || strings.ContainsAny(raw, "/\\?#@ \t\r\n") {
		return ""
	}
	if parsed, err := url.Parse("https://" + raw); err == nil && parsed.Host != "" {
		raw = parsed.Hostname()
	}
	return strings.TrimSuffix(raw, ".")
}

func campaign(metadata map[string]string, query url.Values, referrer string) models.UTMCampaign {
	result := models.UTMCampaign{}
	if parsed, err := url.Parse(strings.TrimSpace(referrer)); err == nil {
		result = models.CampaignFromQuery(parsed.Query())
	}
	if query != nil {
		result = mergeCampaign(result, models.CampaignFromQuery(query))
	}
	for key, target := range map[string]*string{
		"utm_source": &result.Source, "utm_medium": &result.Medium,
		"utm_campaign": &result.Campaign, "utm_content": &result.Content, "utm_term": &result.Term,
	} {
		if value := firstMetadata(metadata, key); value != "" {
			*target = value
		}
	}
	result.Source = strings.ToLower(cleanLabel(result.Source, 200))
	result.Medium = strings.ToLower(cleanLabel(result.Medium, 200))
	result.Campaign = strings.ToLower(cleanLabel(result.Campaign, 200))
	result.Content = strings.ToLower(cleanLabel(result.Content, 200))
	result.Term = strings.ToLower(cleanLabel(result.Term, 200))
	return result
}

func mergeCampaign(base, override models.UTMCampaign) models.UTMCampaign {
	if override.Source != "" {
		base.Source = override.Source
	}
	if override.Medium != "" {
		base.Medium = override.Medium
	}
	if override.Campaign != "" {
		base.Campaign = override.Campaign
	}
	if override.Content != "" {
		base.Content = override.Content
	}
	if override.Term != "" {
		base.Term = override.Term
	}
	return base
}
