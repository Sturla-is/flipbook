package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

// teamMember mirrors the fields the team and settings templates read from
// handlers.adminMemberView and handlers.adminSettingsMember.
type teamMember struct {
	Membership     *models.Membership
	User           *models.User
	AuthMethod     string
	IsLegacyAdmin  bool
	CanSetPassword bool
	PasswordNote   string
}

func teamTemplateMembers() []teamMember {
	joined := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	member := func(id, name string, role models.Role) (*models.Membership, *models.User) {
		return &models.Membership{WorkspaceID: models.DefaultWorkspaceID, UserID: id, Role: role, CreatedAt: joined},
			&models.User{ID: id, Name: name, Email: id + "@example.com"}
	}
	selfMembership, selfUser := member("self", "Signed In Admin", models.RoleAdmin)
	editorMembership, editorUser := member("editor", "Edda Editor", models.RoleEditor)
	ssoMembership, ssoUser := member("sso", "Sigrun SSO", models.RoleReader)
	ownerMembership, ownerUser := member("owner", "Olafur Owner", models.RoleOwner)
	return []teamMember{
		{Membership: selfMembership, User: selfUser, AuthMethod: "Local password"},
		{Membership: editorMembership, User: editorUser, AuthMethod: "Local password", CanSetPassword: true},
		{Membership: ssoMembership, User: ssoUser, AuthMethod: "OIDC", PasswordNote: "Password managed by single sign-on"},
		{Membership: ownerMembership, User: ownerUser, AuthMethod: "Local password", PasswordNote: "Only an owner can set this password"},
	}
}

func renderAdminTemplate(t *testing.T, name string, data map[string]any) string {
	t.Helper()
	base := map[string]any{
		"CSRFToken": "csrf-token", "ActiveNav": "team", "CanManage": true, "CanEdit": true,
		"CurrentRole": models.RoleAdmin, "CurrentUserID": "self", "WorkspaceOwnerID": "owner",
	}
	for key, value := range data {
		base[key] = value
	}
	var output bytes.Buffer
	if err := parseTemplates().ExecuteTemplate(&output, name, base); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestTeamTemplateShowsMemberPasswordControls(t *testing.T) {
	html := renderAdminTemplate(t, "admin_team", map[string]any{"TeamSection": "members", "Members": teamTemplateMembers()})

	for _, expected := range []string{
		`action="/admin/team/members/editor/password"`,
		`id="team-password-editor"`, `id="team-password-confirm-editor"`,
		`Set a new password for Edda Editor? They will be signed out of every session.`,
		`<a href="/account/password">Change my password</a>`,
		"Password managed by single sign-on",
		"Only an owner can set this password",
	} {
		if !strings.Contains(html, expected) {
			t.Errorf("team page does not contain %q", expected)
		}
	}
	for _, userID := range []string{"self", "sso", "owner"} {
		if strings.Contains(html, `action="/admin/team/members/`+userID+`/password"`) {
			t.Errorf("team page offers a set-password form for %s", userID)
		}
	}
}

func TestSettingsTemplateHidesPasswordFormsTheAdminCannotUse(t *testing.T) {
	html := renderAdminTemplate(t, "admin_settings", map[string]any{
		"TeamSection": "settings", "Members": teamTemplateMembers(), "CanSetLegacyPassword": false,
		"InstanceSettings": map[string]any{"SiteName": "", "SessionTTLHours": 168, "LoginAttemptsPerMinute": 10},
		"ServerInfo":       map[string]string{},
	})
	if strings.Contains(html, `action="/admin/settings/password"`) {
		t.Error("primary administrator form shown to an actor who cannot rotate it")
	}
	if !strings.Contains(html, "Only an owner of the default workspace can change the primary administrator password.") {
		t.Error("missing explanation for the hidden primary administrator form")
	}
	if !strings.Contains(html, `action="/admin/settings/admins/editor/password"`) {
		t.Error("settings page lost the set-password form for an allowed member")
	}
	if strings.Contains(html, `action="/admin/settings/admins/owner/password"`) {
		t.Error("settings page offers a set-password form for the owner")
	}
}
