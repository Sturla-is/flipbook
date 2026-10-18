package models

import (
	"testing"
	"time"
)

func TestRolesAndAPIScopes(t *testing.T) {
	if !RoleOwner.CanManage() || !RolePublisher.CanPublish() || RoleEditor.CanPublish() || RoleReader.CanEdit() {
		t.Fatal("role capabilities are incorrect")
	}
	if Role("bogus").AtLeast(RoleReader) {
		t.Fatal("invalid role must not authorize")
	}
	key := APIKey{Scopes: []string{"flipbooks:*", "analytics:read"}}
	if !key.HasScope(ScopeFlipbooksRead) || !key.HasScope(ScopeFlipbooksWrite) || !key.HasScope(ScopeAnalyticsRead) {
		t.Fatal("expected API scopes were not granted")
	}
	if key.HasScope(ScopeWorkspaceAdmin) || key.HasScope(ScopeLeadsRead) {
		t.Fatal("unexpected API scope granted")
	}
}

func TestTokenAndCredentialExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	valid := now.Add(time.Hour)
	if (AccessToken{ExpiresAt: &expired}).Active(now) || !(AccessToken{ExpiresAt: &valid}).Active(now) {
		t.Fatal("access token expiry handling is incorrect")
	}
	if (APIKey{ExpiresAt: &expired}).Active(now) || !(Session{ExpiresAt: valid}).Active(now) {
		t.Fatal("credential expiry handling is incorrect")
	}
	if (Entitlement{Status: EntitlementActive, ExpiresAt: &valid}).Active(now) != true {
		t.Fatal("active entitlement should be valid")
	}
	if HashToken("secret") == HashToken("other") || HashToken("secret") == "secret" {
		t.Fatal("token hashing is not deterministic and one-way")
	}
}
