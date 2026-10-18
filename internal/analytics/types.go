package analytics

import (
	"errors"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

const (
	ConsentRequired  = "required"
	ConsentAnonymous = "anonymous"
	ConsentDisabled  = "disabled"

	PlacementStandalone = "standalone"
	PlacementEmbed      = "embed"

	ConsentCookieName = "flipbook_analytics_consent"
	SessionCookieName = "flipbook_analytics_session"
	ReaderCookieName  = "flipbook_analytics_reader"

	consentGranted = "granted"
	consentDenied  = "denied"

	MaxBatchEvents  = 100
	MaxEventBody    = 256 * 1024
	MaxDurationMS   = int64(60 * 60 * 1000)
	MaxReportEvents = 500000
)

var (
	ErrInvalidEvent      = errors.New("invalid analytics event")
	ErrCollectionBlocked = errors.New("analytics collection is not allowed")
	ErrRangeTooLarge     = errors.New("analytics date range exceeds 366 days")
	ErrResultTooLarge    = errors.New("analytics result is too large; narrow the date range")
)

// IncomingEvent is the public wire contract. Occurrence time and all network
// context are assigned by the server, not trusted from the client.
type IncomingEvent struct {
	Type       string            `json:"type"`
	Page       int               `json:"page,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type IncomingBatch struct {
	Events []IncomingEvent `json:"events"`
}

type Options struct {
	Enabled             bool
	Retention           time.Duration
	ConsentPolicy       string
	AllowCountry        bool
	ReaderIDPepper      string
	IgnoreAdminPreviews bool
}

func (o Options) normalized() Options {
	if o.Retention <= 0 {
		o.Retention = 365 * 24 * time.Hour
	}
	switch o.ConsentPolicy {
	case ConsentRequired, ConsentAnonymous, ConsentDisabled:
	default:
		o.ConsentPolicy = ConsentRequired
	}
	return o
}

// RequestContext contains already-transport-derived request state. It never
// includes an IP address.
type RequestContext struct {
	Referrer     string
	UserAgent    string
	Country      string
	Consented    bool
	OptedOut     bool
	DoNotTrack   bool
	AdminPreview bool
	SessionID    string
	ReaderID     string
}

type Publication struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	PageCount int    `json:"page_count"`
}

type Range struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"` // exclusive
}

type Scope struct {
	WorkspaceID string `json:"workspace_id"`
	FlipbookID  string `json:"flipbook_id,omitempty"`
}

type Totals struct {
	Views                int64   `json:"views"`
	PageViews            int64   `json:"page_views"`
	QualifiedReads       int64   `json:"qualified_reads"`
	Sessions             int64   `json:"sessions"`
	UniqueReaders        int64   `json:"unique_readers"`
	ActiveSeconds        int64   `json:"active_seconds"`
	AverageActiveSeconds float64 `json:"average_active_seconds"`
	Completions          int64   `json:"completions"`
	CompletionRate       float64 `json:"completion_rate"`
	CTAClicks            int64   `json:"cta_clicks"`
	OutboundClicks       int64   `json:"outbound_clicks"`
	Downloads            int64   `json:"downloads"`
	Searches             int64   `json:"searches"`
	Shares               int64   `json:"shares"`
	Leads                int64   `json:"leads"`
	Purchases            int64   `json:"purchases"`
	LegacyViews          int64   `json:"legacy_views"`
}

type PublicationMetrics struct {
	Publication Publication `json:"publication"`
	Totals      Totals      `json:"totals"`
}

type TrendBucket struct {
	Date           string `json:"date"`
	Views          int64  `json:"views"`
	QualifiedReads int64  `json:"qualified_reads"`
	Sessions       int64  `json:"sessions"`
	ActiveSeconds  int64  `json:"active_seconds"`
	Completions    int64  `json:"completions"`
}

type PageMetrics struct {
	Page          int     `json:"page"`
	PageViews     int64   `json:"page_views"`
	Sessions      int64   `json:"sessions"`
	ActiveSeconds int64   `json:"active_seconds"`
	Furthest      int64   `json:"furthest"`
	Completions   int64   `json:"completions"`
	DropOff       int64   `json:"drop_off"`
	DropOffRate   float64 `json:"drop_off_rate"`
	RetentionRate float64 `json:"retention_rate"`
}

type Breakdown struct {
	Key            string `json:"key"`
	Views          int64  `json:"views"`
	QualifiedReads int64  `json:"qualified_reads"`
	Sessions       int64  `json:"sessions"`
	Events         int64  `json:"events"`
	ActiveSeconds  int64  `json:"active_seconds"`
}

type FunnelStep struct {
	Name     string `json:"name"`
	Events   int64  `json:"events"`
	Sessions int64  `json:"sessions"`
}

type Funnel struct {
	Key   string       `json:"key"`
	Steps []FunnelStep `json:"steps"`
}

type Report struct {
	GeneratedAt  time.Time              `json:"generated_at"`
	Range        Range                  `json:"range"`
	Scope        Scope                  `json:"scope"`
	Totals       Totals                 `json:"totals"`
	Publications []PublicationMetrics   `json:"publications"`
	Trends       []TrendBucket          `json:"trends"`
	Pages        []PageMetrics          `json:"pages"`
	Breakdowns   map[string][]Breakdown `json:"breakdowns"`
	Funnel       []FunnelStep           `json:"funnel"`
	Funnels      []Funnel               `json:"funnels"`
}

// PublicEvent is the export representation. Pseudonymous hashes and internal
// expiry fields are intentionally absent.
type PublicEvent struct {
	ID            string                    `json:"id"`
	WorkspaceID   string                    `json:"workspace_id"`
	FlipbookID    string                    `json:"flipbook_id"`
	RevisionID    string                    `json:"revision_id,omitempty"`
	Type          models.AnalyticsEventType `json:"type"`
	OccurredAt    time.Time                 `json:"occurred_at"`
	Page          int                       `json:"page,omitempty"`
	ActiveSeconds int64                     `json:"active_seconds,omitempty"`
	Value         int64                     `json:"value,omitempty"`
	Referrer      string                    `json:"referrer,omitempty"`
	UTM           models.UTMCampaign        `json:"utm"`
	Placement     string                    `json:"placement,omitempty"`
	Device        string                    `json:"device,omitempty"`
	Browser       string                    `json:"browser,omitempty"`
	Country       string                    `json:"country,omitempty"`
	SearchTerms   string                    `json:"search_terms,omitempty"`
	TargetHost    string                    `json:"target_host,omitempty"`
	Metadata      map[string]string         `json:"metadata,omitempty"`
}
