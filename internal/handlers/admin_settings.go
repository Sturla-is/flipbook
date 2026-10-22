package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/auth"
	"github.com/metavert-io/flipbook/internal/models"
)

// settingSiteName is the admin-visible site name shown in the admin panel
// navigation. It is stored in the settings collection and falls back to
// "Penninn Midlun" when unset.
const settingSiteName = "site_name"

const (
	minSessionTTLHours = 1
	maxSessionTTLHours = 720 // 30 days
	maxLoginAttempts   = 1000
)

type adminSettingsMember struct {
	Membership    *models.Membership
	User          *models.User
	AuthMethod    string
	IsLegacyAdmin bool
}

func (h *AdminHandler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderSettings(w, r, actor, http.StatusOK, nil)
}

func (h *AdminHandler) renderSettings(w http.ResponseWriter, r *http.Request, actor adminActor, status int, overrides map[string]any) {
	memberships, err := h.db.ListMemberships(r.Context(), actor.WorkspaceID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	legacyPasswordSet := false
	if h.auth != nil {
		legacyPasswordSet = h.auth.HasPassword()
	}
	members := make([]adminSettingsMember, 0, len(memberships))
	for _, membership := range memberships {
		user, err := h.db.GetUser(r.Context(), membership.UserID)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		members = append(members, adminSettingsMember{
			Membership:    membership,
			User:          user,
			AuthMethod:    adminAuthMethod(user, membership.UserID, legacyPasswordSet),
			IsLegacyAdmin: membership.UserID == models.LegacyAdminUserID,
		})
	}
	siteName, sessionTTLSeconds, loginLimit := h.instanceSettings()
	sessionHours := (sessionTTLSeconds + 3599) / 3600
	if sessionHours < minSessionTTLHours {
		sessionHours = minSessionTTLHours
	}
	if sessionHours > maxSessionTTLHours {
		sessionHours = maxSessionTTLHours
	}
	data := map[string]any{
		"PageTitle":           "Settings - Penninn Midlun",
		"TeamSection":         "settings",
		"Members":             members,
		"LegacyAdminPassword": legacyPasswordSet,
		"CurrentUserID":       actor.UserID,
		"InstanceSettings": map[string]any{
			"SiteName":               siteName,
			"SessionTTLHours":        sessionHours,
			"LoginAttemptsPerMinute": loginLimit,
		},
		"ServerInfo": h.serverInfo(),
	}
	for key, value := range overrides {
		data[key] = value
	}
	if err := h.render(w, r, actor, "team", "admin_settings", status, data); err != nil {
		h.renderError(w, r, err)
	}
}

func adminAuthMethod(user *models.User, userID string, legacyPasswordSet bool) string {
	if user.OIDCIssuer != "" || user.OIDCSubject != "" {
		return "OIDC"
	}
	if user.PasswordHash != "" {
		return "Local password"
	}
	if userID == models.LegacyAdminUserID && legacyPasswordSet {
		return "Local password"
	}
	return "No local password"
}

// instanceSettings returns the current runtime instance settings, falling back
// to the server configuration when no stored override exists.
func (h *AdminHandler) instanceSettings() (siteName string, sessionTTLSeconds, loginAttemptsPerMinute int) {
	sessionTTLSeconds = 7 * 24 * 3600
	loginAttemptsPerMinute = 10
	if h.cfg != nil {
		sessionTTLSeconds = h.cfg.Security.SessionTTLSeconds
		loginAttemptsPerMinute = h.cfg.Security.LoginAttemptsPerMinute
	}
	if h.db != nil {
		if value, err := h.db.GetSetting(settingSiteName); err == nil {
			siteName = strings.TrimSpace(value)
		}
		if value, err := h.db.GetSetting(auth.SettingSessionTTLSeconds); err == nil {
			if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 3600 && seconds <= 30*24*3600 {
				sessionTTLSeconds = seconds
			}
		}
		if value, err := h.db.GetSetting(auth.SettingLoginAttemptsPerMinute); err == nil {
			if limit, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && limit >= 1 && limit <= maxLoginAttempts {
				loginAttemptsPerMinute = limit
			}
		}
	}
	return siteName, sessionTTLSeconds, loginAttemptsPerMinute
}

// serverInfo reports read-only server configuration loaded at startup.
func (h *AdminHandler) serverInfo() map[string]string {
	info := map[string]string{
		"Mode":            "Single-tenant admin",
		"Base URL":        h.baseURL,
		"Max upload size": formatAdminBytes(h.maxBytes),
	}
	if h.cfg != nil {
		if h.cfg.SaaS.Enabled {
			info["Mode"] = "SaaS multi-tenant"
		}
		info["Port"] = h.cfg.Port
		info["Data directory"] = h.cfg.DataDir
		info["LibreOffice"] = h.cfg.LibreOfficeBin
		info["MongoDB database"] = h.cfg.MongoDB
		info["Session TTL default"] = fmt.Sprintf("%d seconds", h.cfg.Security.SessionTTLSeconds)
		info["Login limit default"] = fmt.Sprintf("%d per minute", h.cfg.Security.LoginAttemptsPerMinute)
	}
	return info
}

func formatAdminBytes(size int64) string {
	const unit = 1000
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	divisor, exponent := int64(unit), 0
	for value := size / unit; value >= unit; value /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(divisor), "kMGTPE"[exponent])
}

// CreateUserAccount provisions a named local account with a workspace
// membership directly from the settings page.
func (h *AdminHandler) CreateUserAccount(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderSettings(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "Account management requires authentication service wiring."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The account form is invalid."})
		return
	}
	role := models.Role(strings.TrimSpace(r.FormValue("role")))
	if !role.Valid() || role == models.RoleOwner {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Choose reader, editor, publisher, or admin for the account."})
		return
	}
	if r.FormValue("password") != r.FormValue("confirm_password") {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Passwords do not match."})
		return
	}
	user, err := h.auth.CreateLocalUser(r.Context(), actor.WorkspaceID, actor.UserID,
		r.FormValue("name"), r.FormValue("email"), r.FormValue("password"), role)
	if err != nil {
		status, message := adminErrorMessage(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadRequest
			message = "The account could not be created. Check the name, email, and password."
		}
		h.renderSettings(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	h.renderSettings(w, r, actor, http.StatusCreated, map[string]any{
		"Notice": fmt.Sprintf("Account created for %s with the %s role.", user.Email, role),
	})
}

// SetUserPassword resets the local password of a workspace member. Resetting
// your own password signs out your current session.
func (h *AdminHandler) SetUserPassword(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderSettings(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "Password management requires authentication service wiring."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The password form is invalid."})
		return
	}
	userID := chi.URLParam(r, "userID")
	if r.FormValue("password") != r.FormValue("confirm_password") {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Passwords do not match."})
		return
	}
	if err := h.auth.SetUserPasswordByAdmin(r.Context(), actor.WorkspaceID, actor.UserID, userID, r.FormValue("password")); err != nil {
		status, message := adminErrorMessage(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadRequest
			message = "The password could not be changed."
		}
		h.renderSettings(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	if userID == actor.UserID {
		http.Redirect(w, r, "/login?notice=password-changed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/settings?notice="+url.QueryEscape("Password changed. The user's existing sessions were signed out."), http.StatusSeeOther)
}

// SetLegacyAdminPassword rotates the primary "admin" login password. Changing
// it while signed in as that account signs out the current session.
func (h *AdminHandler) SetLegacyAdminPassword(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderSettings(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "Password management requires authentication service wiring."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The password form is invalid."})
		return
	}
	if r.FormValue("password") != r.FormValue("confirm_password") {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Passwords do not match."})
		return
	}
	if err := h.auth.SetLegacyAdminPassword(r.Context(), actor.WorkspaceID, actor.UserID, r.FormValue("password")); err != nil {
		status, message := adminErrorMessage(err)
		if status == http.StatusInternalServerError {
			status = http.StatusBadRequest
			message = "The administrator password could not be changed."
		}
		h.renderSettings(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	if actor.UserID == models.LegacyAdminUserID {
		http.Redirect(w, r, "/login?notice=password-changed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/settings?notice="+url.QueryEscape("Primary administrator password changed."), http.StatusSeeOther)
}

// UpdateInstanceSettings persists the runtime instance settings to the
// settings collection. Session duration and login limits apply immediately to
// new sessions and sign-in attempts.
func (h *AdminHandler) UpdateInstanceSettings(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The instance settings form is invalid."})
		return
	}
	siteName := strings.TrimSpace(r.FormValue("site_name"))
	if len(siteName) > 100 || strings.ContainsAny(siteName, "\r\n\x00") {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The site name must be at most 100 characters."})
		return
	}
	if strings.TrimSpace(r.FormValue("session_ttl_hours")) == "" {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Session duration is required."})
		return
	}
	sessionHours, err := parseAdminInt(r.FormValue("session_ttl_hours"), "Session duration", minSessionTTLHours, maxSessionTTLHours)
	if err != nil {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	if strings.TrimSpace(r.FormValue("login_attempts_per_minute")) == "" {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Login attempts per minute is required."})
		return
	}
	loginLimit, err := parseAdminInt(r.FormValue("login_attempts_per_minute"), "Login attempts per minute", 1, maxLoginAttempts)
	if err != nil {
		h.renderSettings(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	if err := h.db.SetSetting(settingSiteName, siteName); err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := h.db.SetSetting(auth.SettingSessionTTLSeconds, strconv.Itoa(sessionHours*3600)); err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := h.db.SetSetting(auth.SettingLoginAttemptsPerMinute, strconv.Itoa(loginLimit)); err != nil {
		h.renderError(w, r, err)
		return
	}
	h.audit(r, actor, "settings.instance.update", "settings", "", "success", map[string]string{
		"site_name": siteName, "session_ttl_hours": strconv.Itoa(sessionHours),
		"login_attempts_per_minute": strconv.Itoa(loginLimit),
	})
	http.Redirect(w, r, "/admin/settings?notice="+url.QueryEscape("Instance settings saved."), http.StatusSeeOther)
}
