package models

import "time"

const (
	StatusPending      = "pending"
	StatusConverting   = "converting"
	StatusReady        = "ready"
	StatusError        = "error"
	StatusRegenerating = "regenerating"
)

type Flipbook struct {
	ID           string
	Title        string
	Slug         string
	Description  string
	Filename     string
	FileSize     int64
	PageCount    int
	Status       string
	ErrorMessage string
	PageWidth    int
	PageHeight   int
	IsPublic     bool
	GridFSFileID string
	TenantID     string // Owning tenant (empty = legacy admin flipbook)
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ConvertedAt  *time.Time
}
