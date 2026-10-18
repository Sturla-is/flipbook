package models

import (
	"net/url"
	"strings"
	"time"
)

type AnalyticsEventType string

const (
	AnalyticsView          AnalyticsEventType = "view"
	AnalyticsQualifiedRead AnalyticsEventType = "qualified_read"
	AnalyticsPageExposure  AnalyticsEventType = "page_exposure"
	AnalyticsActiveTime    AnalyticsEventType = "active_time"
	AnalyticsFurthestPage  AnalyticsEventType = "furthest_page"
	AnalyticsCompletion    AnalyticsEventType = "completion"
	AnalyticsCTAClick      AnalyticsEventType = "cta_click"
	AnalyticsOutboundClick AnalyticsEventType = "outbound_click"
	AnalyticsDownload      AnalyticsEventType = "download"
	AnalyticsSearch        AnalyticsEventType = "search"
	AnalyticsShare         AnalyticsEventType = "share"
	AnalyticsLead          AnalyticsEventType = "lead"
	AnalyticsPurchase      AnalyticsEventType = "purchase"
)

type UTMCampaign struct {
	Source   string `bson:"source,omitempty" json:"source,omitempty"`
	Medium   string `bson:"medium,omitempty" json:"medium,omitempty"`
	Campaign string `bson:"campaign,omitempty" json:"campaign,omitempty"`
	Content  string `bson:"content,omitempty" json:"content,omitempty"`
	Term     string `bson:"term,omitempty" json:"term,omitempty"`
}

type AnalyticsEvent struct {
	ID            string             `bson:"_id" json:"id"`
	WorkspaceID   string             `bson:"workspace_id" json:"workspace_id"`
	FlipbookID    string             `bson:"flipbook_id" json:"flipbook_id"`
	RevisionID    string             `bson:"revision_id,omitempty" json:"revision_id,omitempty"`
	Type          AnalyticsEventType `bson:"type" json:"type"`
	OccurredAt    time.Time          `bson:"occurred_at" json:"occurred_at"`
	ExpiresAt     time.Time          `bson:"expires_at" json:"expires_at"`
	SessionIDHash string             `bson:"session_id_hash,omitempty" json:"session_id_hash,omitempty"`
	ReaderIDHash  string             `bson:"reader_id_hash,omitempty" json:"reader_id_hash,omitempty"`
	Page          int                `bson:"page,omitempty" json:"page,omitempty"`
	ActiveSeconds int64              `bson:"active_seconds,omitempty" json:"active_seconds,omitempty"`
	Value         int64              `bson:"value,omitempty" json:"value,omitempty"`
	Referrer      string             `bson:"referrer,omitempty" json:"referrer,omitempty"`
	UTM           UTMCampaign        `bson:"utm" json:"utm"`
	Placement     string             `bson:"placement,omitempty" json:"placement,omitempty"`
	Device        string             `bson:"device,omitempty" json:"device,omitempty"`
	Browser       string             `bson:"browser,omitempty" json:"browser,omitempty"`
	Country       string             `bson:"country,omitempty" json:"country,omitempty"`
	SearchTerms   string             `bson:"search_terms,omitempty" json:"search_terms,omitempty"`
	TargetHost    string             `bson:"target_host,omitempty" json:"target_host,omitempty"`
	Metadata      map[string]string  `bson:"metadata,omitempty" json:"metadata,omitempty"`
}

func (e *AnalyticsEvent) PrepareForStorage(retention time.Duration, consentPolicy string, consented bool) {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if retention <= 0 {
		retention = 365 * 24 * time.Hour
	}
	e.ExpiresAt = e.OccurredAt.Add(retention)
	e.Referrer = NormalizeReferrer(e.Referrer)
	e.Country = strings.ToUpper(strings.TrimSpace(e.Country))
	if len(e.Country) != 2 {
		e.Country = ""
	}
	if consentPolicy == "required" && !consented || consentPolicy == "disabled" {
		e.SessionIDHash = ""
		e.ReaderIDHash = ""
	}
}

func NormalizeReferrer(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Host == "" && (u.Path == "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//")) {
		return ""
	}
	if u.Host != "" && (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.User = nil
	if u.Host != "" {
		u.Host = strings.ToLower(u.Host)
		u.Scheme = strings.ToLower(u.Scheme)
	}
	return u.String()
}

func CampaignFromQuery(values url.Values) UTMCampaign {
	clean := func(key string) string {
		return strings.TrimSpace(strings.ToLower(values.Get(key)))
	}
	return UTMCampaign{
		Source: clean("utm_source"), Medium: clean("utm_medium"),
		Campaign: clean("utm_campaign"), Content: clean("utm_content"), Term: clean("utm_term"),
	}
}

func CoarseDeviceBrowser(userAgent string) (device, browser string) {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "ipad"), strings.Contains(ua, "tablet"):
		device = "tablet"
	case strings.Contains(ua, "mobile"), strings.Contains(ua, "iphone"), strings.Contains(ua, "android"):
		device = "mobile"
	default:
		device = "desktop"
	}
	switch {
	case strings.Contains(ua, "edg/"):
		browser = "edge"
	case strings.Contains(ua, "firefox/"):
		browser = "firefox"
	case strings.Contains(ua, "chrome/"), strings.Contains(ua, "crios/"):
		browser = "chrome"
	case strings.Contains(ua, "safari/"):
		browser = "safari"
	default:
		browser = "other"
	}
	return device, browser
}

type AnalyticsFilter struct {
	WorkspaceID string
	FlipbookIDs []string
	Types       []AnalyticsEventType
	From        time.Time
	To          time.Time
}

type AnalyticsSummary struct {
	Events         int64   `bson:"events" json:"events"`
	QualifiedReads int64   `bson:"qualified_reads" json:"qualified_reads"`
	UniqueReaders  int64   `bson:"unique_readers" json:"unique_readers"`
	ActiveSeconds  int64   `bson:"active_seconds" json:"active_seconds"`
	Downloads      int64   `bson:"downloads" json:"downloads"`
	Leads          int64   `bson:"leads" json:"leads"`
	Purchases      int64   `bson:"purchases" json:"purchases"`
	Completions    int64   `bson:"completions" json:"completions"`
	CompletionRate float64 `bson:"completion_rate" json:"completion_rate"`
}

type AnalyticsBucket struct {
	Key   string `bson:"_id" json:"key"`
	Count int64  `bson:"count" json:"count"`
	Value int64  `bson:"value" json:"value"`
}

type AnalyticsPageStat struct {
	Page          int   `bson:"_id" json:"page"`
	Exposures     int64 `bson:"exposures" json:"exposures"`
	Completions   int64 `bson:"completions" json:"completions"`
	ActiveSeconds int64 `bson:"active_seconds" json:"active_seconds"`
}
