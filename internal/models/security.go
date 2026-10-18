package models

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type Session struct {
	TokenHash   string     `bson:"_id" json:"-"`
	UserID      string     `bson:"user_id,omitempty" json:"user_id,omitempty"`
	WorkspaceID string     `bson:"workspace_id,omitempty" json:"workspace_id,omitempty"`
	CreatedAt   time.Time  `bson:"created_at" json:"created_at"`
	ExpiresAt   time.Time  `bson:"expires_at" json:"expires_at"`
	LastSeenAt  *time.Time `bson:"last_seen_at,omitempty" json:"last_seen_at,omitempty"`
	RevokedAt   *time.Time `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
}

func (s Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

const (
	ScopeFlipbooksRead     = "flipbooks:read"
	ScopeFlipbooksWrite    = "flipbooks:write"
	ScopeAnalyticsRead     = "analytics:read"
	ScopeLeadsRead         = "leads:read"
	ScopeLeadsWrite        = "leads:write"
	ScopeSalesRead         = "sales:read"
	ScopeIntegrationsWrite = "integrations:write"
	ScopeWorkspaceAdmin    = "workspace:admin"
)

type APIKey struct {
	ID          string     `bson:"_id" json:"id"`
	WorkspaceID string     `bson:"workspace_id" json:"workspace_id"`
	UserID      string     `bson:"user_id" json:"user_id"`
	Name        string     `bson:"name" json:"name"`
	Prefix      string     `bson:"prefix" json:"prefix"`
	KeyHash     string     `bson:"key_hash" json:"-"`
	Scopes      []string   `bson:"scopes" json:"scopes"`
	CreatedAt   time.Time  `bson:"created_at" json:"created_at"`
	ExpiresAt   *time.Time `bson:"expires_at,omitempty" json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `bson:"last_used_at,omitempty" json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
}

func (k APIKey) Active(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

func (k APIKey) HasScope(required string) bool {
	for _, scope := range k.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "*" || scope == required {
			return true
		}
		if strings.HasSuffix(scope, ":*") && strings.HasPrefix(required, strings.TrimSuffix(scope, "*")) {
			return true
		}
	}
	return false
}

type AuditEvent struct {
	ID            string            `bson:"_id" json:"id"`
	WorkspaceID   string            `bson:"workspace_id" json:"workspace_id"`
	ActorUserID   string            `bson:"actor_user_id,omitempty" json:"actor_user_id,omitempty"`
	ActorAPIKeyID string            `bson:"actor_api_key_id,omitempty" json:"actor_api_key_id,omitempty"`
	Action        string            `bson:"action" json:"action"`
	ResourceType  string            `bson:"resource_type,omitempty" json:"resource_type,omitempty"`
	ResourceID    string            `bson:"resource_id,omitempty" json:"resource_id,omitempty"`
	Outcome       string            `bson:"outcome" json:"outcome"`
	RequestID     string            `bson:"request_id,omitempty" json:"request_id,omitempty"`
	Metadata      map[string]string `bson:"metadata,omitempty" json:"metadata,omitempty"`
	CreatedAt     time.Time         `bson:"created_at" json:"created_at"`
}
