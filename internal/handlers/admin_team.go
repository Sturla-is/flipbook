package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/models"
)

type adminMemberView struct {
	Membership *models.Membership
	User       *models.User
}

func (h *AdminHandler) Team(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderTeam(w, r, actor, http.StatusOK, nil)
}

func (h *AdminHandler) renderTeam(w http.ResponseWriter, r *http.Request, actor adminActor, status int, overrides map[string]any) {
	memberships, err := h.db.ListMemberships(r.Context(), actor.WorkspaceID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	members := make([]adminMemberView, 0, len(memberships))
	for _, membership := range memberships {
		user, err := h.db.GetUser(r.Context(), membership.UserID)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		members = append(members, adminMemberView{Membership: membership, User: user})
	}
	workspace, err := h.db.GetWorkspace(r.Context(), actor.WorkspaceID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	data := map[string]any{
		"PageTitle": "Team and access - Penninn Midlun", "TeamSection": "members", "Members": members,
		"WorkspaceOwnerID": workspace.OwnerID, "CurrentUserID": actor.UserID,
	}
	for key, value := range overrides {
		data[key] = value
	}
	if err := h.render(w, r, actor, "team", "admin_team", status, data); err != nil {
		h.renderError(w, r, err)
	}
}

func (h *AdminHandler) InviteMember(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderTeam(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "Invitation service wiring is required."})
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The invitation form is invalid."})
		return
	}
	email, err := validateAdminEmail(r.FormValue("email"))
	if err != nil {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	role := models.Role(r.FormValue("role"))
	if !role.Valid() || role == models.RoleOwner {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Choose reader, editor, publisher, or admin for the invitation."})
		return
	}
	days, err := parseAdminInt(r.FormValue("expires_in_days"), "Invitation validity", 7, 30)
	if err != nil {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	token, invitation, err := h.auth.CreateInvitation(r.Context(), actor.WorkspaceID, actor.UserID, email, role, time.Duration(days)*24*time.Hour)
	if err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	invitationURL := h.baseURL + "/invitation?token=" + url.QueryEscape(token)
	h.renderTeam(w, r, actor, http.StatusCreated, map[string]any{
		"Notice":        "Invitation created. Send this one-time link to the invited user; it will not be shown again.",
		"InvitationURL": invitationURL, "NewInvitation": invitation,
	})
}

func (h *AdminHandler) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if h.auth == nil {
		h.renderTeam(w, r, actor, http.StatusServiceUnavailable, map[string]any{"Error": "Invitation service wiring is required."})
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.auth.RevokeInvitation(r.Context(), actor.WorkspaceID, actor.UserID, id); err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	http.Redirect(w, r, "/admin/team?notice="+url.QueryEscape("Invitation revoked."), http.StatusSeeOther)
}

func (h *AdminHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The role form is invalid."})
		return
	}
	userID := chi.URLParam(r, "userID")
	role := models.Role(r.FormValue("role"))
	if !role.Valid() || role == models.RoleOwner {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Roles can be reader, editor, publisher, or admin. Ownership uses a separate transfer flow."})
		return
	}
	membership, err := h.db.GetMembership(r.Context(), actor.WorkspaceID, userID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if membership.Role == models.RoleOwner {
		h.renderTeam(w, r, actor, http.StatusForbidden, map[string]any{"Error": "An owner's role cannot be changed through the member-role form."})
		return
	}
	if !actor.Role.AtLeast(membership.Role) || !actor.Role.AtLeast(role) {
		h.renderError(w, r, errAdminForbidden)
		return
	}
	membership.Role = role
	if err := h.db.UpsertMembership(r.Context(), membership); err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	h.audit(r, actor, "workspace.member.role", "user", userID, "success", map[string]string{"role": string(role)})
	http.Redirect(w, r, "/admin/team?notice="+url.QueryEscape("Member role updated."), http.StatusSeeOther)
}

func (h *AdminHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	userID := chi.URLParam(r, "userID")
	membership, err := h.db.GetMembership(r.Context(), actor.WorkspaceID, userID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	workspace, err := h.db.GetWorkspace(r.Context(), actor.WorkspaceID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if userID == workspace.OwnerID || (membership.Role == models.RoleOwner && actor.Role != models.RoleOwner) {
		h.renderTeam(w, r, actor, http.StatusConflict, map[string]any{"Error": "The workspace owner cannot be removed. Transfer ownership first."})
		return
	}
	if err := h.db.RemoveMembership(r.Context(), actor.WorkspaceID, userID); err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	h.audit(r, actor, "workspace.member.remove", "user", userID, "success", nil)
	http.Redirect(w, r, "/admin/team?notice="+url.QueryEscape("Member removed from this workspace."), http.StatusSeeOther)
}

func (h *AdminHandler) SetMemberDisabled(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The account-status form is invalid."})
		return
	}
	userID := chi.URLParam(r, "userID")
	membership, err := h.db.GetMembership(r.Context(), actor.WorkspaceID, userID)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if userID == actor.UserID || membership.Role == models.RoleOwner {
		h.renderTeam(w, r, actor, http.StatusConflict, map[string]any{"Error": "Your own account and workspace owners cannot be disabled from this screen."})
		return
	}
	disabled := adminFormBool(r, "disabled")
	if err := h.db.SetUserDisabled(r.Context(), userID, disabled); err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	outcome := "enabled"
	if disabled {
		outcome = "disabled"
	}
	h.audit(r, actor, "workspace.member."+outcome, "user", userID, "success", nil)
	http.Redirect(w, r, "/admin/team?notice="+url.QueryEscape("Account "+outcome+" across all workspaces."), http.StatusSeeOther)
}

func (h *AdminHandler) RevokeMemberSessions(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	userID := chi.URLParam(r, "userID")
	if h.auth != nil {
		err = h.auth.RevokeUserSessions(r.Context(), actor.WorkspaceID, actor.UserID, userID)
	} else {
		if _, err = h.db.GetMembership(r.Context(), actor.WorkspaceID, userID); err == nil {
			err = h.db.RevokeUserSessions(r.Context(), userID)
		}
	}
	if err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	if h.auth == nil {
		h.audit(r, actor, "auth.sessions.revoke_all", "user", userID, "success", nil)
	}
	http.Redirect(w, r, "/admin/team?notice="+url.QueryEscape("All sessions revoked for this user."), http.StatusSeeOther)
}

func (h *AdminHandler) UpdateMemberProfile(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleAdmin)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "The profile form is invalid."})
		return
	}
	userID := chi.URLParam(r, "userID")
	if _, err := h.db.GetMembership(r.Context(), actor.WorkspaceID, userID); err != nil {
		h.renderError(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	locale, err := validateAdminLanguage(r.FormValue("locale"))
	if err != nil || name == "" || len(name) > 200 {
		h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "A valid name and locale are required."})
		return
	}
	timeZone := strings.TrimSpace(r.FormValue("time_zone"))
	if timeZone != "" {
		if len(timeZone) > 100 {
			h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Time zone is too long."})
			return
		}
		if _, err := time.LoadLocation(timeZone); err != nil {
			h.renderTeam(w, r, actor, http.StatusBadRequest, map[string]any{"Error": "Enter a valid IANA time zone such as Europe/Oslo."})
			return
		}
	}
	if err := h.db.UpdateUserProfile(r.Context(), userID, name, locale, timeZone); err != nil {
		status, message := adminErrorMessage(err)
		h.renderTeam(w, r, actor, status, map[string]any{"Error": message})
		return
	}
	h.audit(r, actor, "workspace.member.profile", "user", userID, "success", nil)
	http.Redirect(w, r, "/admin/team?notice="+url.QueryEscape("User profile updated."), http.StatusSeeOther)
}
