package migration

import (
	"context"
	"io"
	"time"
)

const (
	StatusPlanned   = "planned"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusExisting  = "existing"
	StatusSkipped   = "skipped"
	StatusFailed    = "failed"
)

type Item struct {
	SourcePath    string   `json:"source_path"`
	RelativePath  string   `json:"relative_path"`
	ExternalID    string   `json:"external_id"`
	Title         string   `json:"title"`
	DesiredSlug   string   `json:"desired_slug"`
	LegacySlug    string   `json:"legacy_slug,omitempty"`
	LegacyURL     string   `json:"legacy_url,omitempty"`
	Description   string   `json:"description,omitempty"`
	Language      string   `json:"language,omitempty"`
	Visibility    string   `json:"visibility,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	CollectionIDs []string `json:"collection_ids,omitempty"`
	Publish       bool     `json:"publish"`
	SHA256        string   `json:"sha256"`
	Size          int64    `json:"size"`
}

type ReportItem struct {
	ExternalID        string     `json:"external_id"`
	SourcePath        string     `json:"source_path"`
	Title             string     `json:"title"`
	DesiredSlug       string     `json:"desired_slug"`
	LegacySlug        string     `json:"legacy_slug,omitempty"`
	LegacyURL         string     `json:"legacy_url,omitempty"`
	LegacyRedirectURL string     `json:"legacy_redirect_url,omitempty"`
	FlipbookID        string     `json:"flipbook_id,omitempty"`
	ViewerURL         string     `json:"viewer_url,omitempty"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	Error             string     `json:"error,omitempty"`
	SHA256            string     `json:"sha256"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type Report struct {
	SchemaVersion int          `json:"schema_version"`
	RunID         string       `json:"run_id"`
	StartedAt     time.Time    `json:"started_at"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
	DryRun        bool         `json:"dry_run"`
	Total         int          `json:"total"`
	Succeeded     int          `json:"succeeded"`
	Existing      int          `json:"existing"`
	Skipped       int          `json:"skipped"`
	Failed        int          `json:"failed"`
	Planned       int          `json:"planned,omitempty"`
	Items         []ReportItem `json:"items"`
}

func (r *Report) recount() {
	r.Succeeded, r.Existing, r.Skipped, r.Failed, r.Planned = 0, 0, 0, 0, 0
	for _, item := range r.Items {
		switch item.Status {
		case StatusSucceeded:
			r.Succeeded++
		case StatusExisting:
			r.Existing++
		case StatusSkipped:
			r.Skipped++
		case StatusFailed:
			r.Failed++
		case StatusPlanned:
			r.Planned++
		}
	}
	r.Total = len(r.Items)
}

type Flipbook struct {
	ID                string `json:"id"`
	Slug              string `json:"slug"`
	Status            string `json:"status"`
	PublicationState  string `json:"publication_state"`
	PageCount         int    `json:"page_count"`
	CurrentRevisionID string `json:"current_revision_id"`
	AssetVersion      int64  `json:"asset_version"`
	Error             string `json:"error"`
}

type UploadResult struct {
	Flipbook   Flipbook
	JobID      string
	RevisionID string
	Replay     bool
}

type API interface {
	Create(ctx context.Context, item Item, initialSlug string, source io.Reader) (UploadResult, error)
	Replace(ctx context.Context, flipbookID string, item Item, source io.Reader) (UploadResult, error)
	Status(ctx context.Context, flipbookID string) (Flipbook, error)
	ChangeSlug(ctx context.Context, flipbookID, desiredSlug string) (Flipbook, error)
	Publish(ctx context.Context, flipbookID, visibility string) (Flipbook, error)
}

type Options struct {
	Folder         string
	CSVFile        string
	ReportPath     string
	StatePath      string
	Resume         bool
	DryRun         bool
	UpdateExisting bool
	Publish        bool
	Visibility     string
	Concurrency    int
	MaxAttempts    int
	WaitTimeout    time.Duration
	PollInterval   time.Duration
	Now            func() time.Time
}

type Runner struct {
	api     API
	options Options
	baseURL string
}
