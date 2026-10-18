package models

import "time"

type Collection struct {
	ID              string      `bson:"_id" json:"id"`
	WorkspaceID     string      `bson:"workspace_id" json:"workspace_id"`
	Name            string      `bson:"name" json:"name"`
	Slug            string      `bson:"slug" json:"slug"`
	Description     string      `bson:"description,omitempty" json:"description,omitempty"`
	Visibility      Visibility  `bson:"visibility" json:"visibility"`
	FlipbookIDs     []string    `bson:"flipbook_ids" json:"flipbook_ids"`
	CoverFlipbookID string      `bson:"cover_flipbook_id,omitempty" json:"cover_flipbook_id,omitempty"`
	SEO             SEOMetadata `bson:"seo" json:"seo"`
	CreatedBy       string      `bson:"created_by,omitempty" json:"created_by,omitempty"`
	CreatedAt       time.Time   `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time   `bson:"updated_at" json:"updated_at"`
}

type SlugRedirect struct {
	ID          string    `bson:"_id" json:"id"`
	WorkspaceID string    `bson:"workspace_id" json:"workspace_id"`
	Slug        string    `bson:"slug" json:"slug"`
	FlipbookID  string    `bson:"flipbook_id" json:"flipbook_id"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
}
