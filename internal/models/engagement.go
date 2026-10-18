package models

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type LinkType string

const (
	LinkExternal LinkType = "external"
	LinkInternal LinkType = "internal"
	LinkVideo    LinkType = "video"
)

type LinkOverlay struct {
	ID          string    `bson:"_id" json:"id"`
	WorkspaceID string    `bson:"workspace_id" json:"workspace_id"`
	FlipbookID  string    `bson:"flipbook_id" json:"flipbook_id"`
	RevisionID  string    `bson:"revision_id,omitempty" json:"revision_id,omitempty"`
	Page        int       `bson:"page" json:"page"`
	X           float64   `bson:"x" json:"x"`
	Y           float64   `bson:"y" json:"y"`
	Width       float64   `bson:"width" json:"width"`
	Height      float64   `bson:"height" json:"height"`
	Type        LinkType  `bson:"type" json:"type"`
	URL         string    `bson:"url,omitempty" json:"url,omitempty"`
	TargetPage  int       `bson:"target_page,omitempty" json:"target_page,omitempty"`
	Title       string    `bson:"title,omitempty" json:"title,omitempty"`
	Enabled     bool      `bson:"enabled" json:"enabled"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`
}

func (l LinkOverlay) Validate() error {
	if l.FlipbookID == "" || l.Page < 1 {
		return errors.New("flipbook and a positive page are required")
	}
	if l.X < 0 || l.Y < 0 || l.Width <= 0 || l.Height <= 0 || l.X+l.Width > 1 || l.Y+l.Height > 1 {
		return errors.New("link rectangle must be within normalized page bounds")
	}
	switch l.Type {
	case LinkInternal:
		if l.TargetPage < 1 {
			return errors.New("internal links require a positive target page")
		}
	case LinkExternal, LinkVideo:
		u, err := url.Parse(l.URL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("external links require an absolute http or https URL")
		}
	default:
		return errors.New("unsupported link type")
	}
	return nil
}

type Lead struct {
	ID                 string            `bson:"_id" json:"id"`
	WorkspaceID        string            `bson:"workspace_id" json:"workspace_id"`
	FlipbookID         string            `bson:"flipbook_id" json:"flipbook_id"`
	Email              string            `bson:"email" json:"email"`
	NormalizedEmail    string            `bson:"normalized_email" json:"-"`
	Name               string            `bson:"name,omitempty" json:"name,omitempty"`
	Consented          bool              `bson:"consented" json:"consented"`
	ConsentText        string            `bson:"consent_text,omitempty" json:"consent_text,omitempty"`
	ConsentedAt        *time.Time        `bson:"consented_at,omitempty" json:"consented_at,omitempty"`
	Source             string            `bson:"source,omitempty" json:"source,omitempty"`
	UTM                UTMCampaign       `bson:"utm" json:"utm"`
	Metadata           map[string]string `bson:"metadata,omitempty" json:"metadata,omitempty"`
	RetentionExpiresAt *time.Time        `bson:"retention_expires_at,omitempty" json:"retention_expires_at,omitempty"`
	CreatedAt          time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time         `bson:"updated_at" json:"updated_at"`
	DeletedAt          *time.Time        `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

func (l *Lead) Normalize() {
	l.NormalizedEmail = strings.ToLower(strings.TrimSpace(l.Email))
}

type SubscriberStatus string

const (
	SubscriberPending      SubscriberStatus = "pending"
	SubscriberSubscribed   SubscriberStatus = "subscribed"
	SubscriberUnsubscribed SubscriberStatus = "unsubscribed"
)

type Subscriber struct {
	ID                    string           `bson:"_id" json:"id"`
	WorkspaceID           string           `bson:"workspace_id" json:"workspace_id"`
	Email                 string           `bson:"email" json:"email"`
	NormalizedEmail       string           `bson:"normalized_email" json:"-"`
	Status                SubscriberStatus `bson:"status" json:"status"`
	Source                string           `bson:"source,omitempty" json:"source,omitempty"`
	ConfirmationTokenHash string           `bson:"confirmation_token_hash" json:"-"`
	UnsubscribeTokenHash  string           `bson:"unsubscribe_token_hash" json:"-"`
	ConfirmationExpiresAt *time.Time       `bson:"confirmation_expires_at,omitempty" json:"confirmation_expires_at,omitempty"`
	ConfirmedAt           *time.Time       `bson:"confirmed_at,omitempty" json:"confirmed_at,omitempty"`
	UnsubscribedAt        *time.Time       `bson:"unsubscribed_at,omitempty" json:"unsubscribed_at,omitempty"`
	CreatedAt             time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt             time.Time        `bson:"updated_at" json:"updated_at"`
}

func (s *Subscriber) Normalize() {
	s.NormalizedEmail = strings.ToLower(strings.TrimSpace(s.Email))
}
