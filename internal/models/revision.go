package models

import "time"

const (
	RevisionPending    = "pending"
	RevisionConverting = "converting"
	RevisionReady      = "ready"
	RevisionActive     = "active"
	RevisionFailed     = "failed"
	RevisionSuperseded = "superseded"
)

type Revision struct {
	ID                   string     `bson:"_id" json:"id"`
	WorkspaceID          string     `bson:"workspace_id" json:"workspace_id"`
	FlipbookID           string     `bson:"flipbook_id" json:"flipbook_id"`
	Number               int        `bson:"number" json:"number"`
	AssetVersion         int64      `bson:"asset_version" json:"asset_version"`
	Status               string     `bson:"status" json:"status"`
	SourceFilename       string     `bson:"source_filename,omitempty" json:"source_filename,omitempty"`
	SourceKey            string     `bson:"source_key,omitempty" json:"source_key,omitempty"`
	StagingKey           string     `bson:"staging_key,omitempty" json:"staging_key,omitempty"`
	ManifestKey          string     `bson:"manifest_key,omitempty" json:"manifest_key,omitempty"`
	OriginalGridFSFileID string     `bson:"original_gridfs_file_id,omitempty" json:"original_gridfs_file_id,omitempty"`
	SourceChecksum       string     `bson:"source_checksum,omitempty" json:"source_checksum,omitempty"`
	ExternalSource       string     `bson:"external_source,omitempty" json:"external_source,omitempty"`
	ExternalID           string     `bson:"external_id,omitempty" json:"external_id,omitempty"`
	PageCount            int        `bson:"page_count" json:"page_count"`
	Progress             int        `bson:"progress" json:"progress"`
	Warnings             []string   `bson:"warnings,omitempty" json:"warnings,omitempty"`
	ErrorMessage         string     `bson:"error_message,omitempty" json:"error_message,omitempty"`
	CreatedBy            string     `bson:"created_by,omitempty" json:"created_by,omitempty"`
	CreatedAt            time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt            time.Time  `bson:"updated_at" json:"updated_at"`
	ValidatedAt          *time.Time `bson:"validated_at,omitempty" json:"validated_at,omitempty"`
	ActivatedAt          *time.Time `bson:"activated_at,omitempty" json:"activated_at,omitempty"`
	DeletedAt            *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
