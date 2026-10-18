package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type apiIdentityContextKey struct{}

type apiIdentity struct {
	UserID      string
	WorkspaceID string
	Role        models.Role
	APIKey      *models.APIKey
}

func (i *apiIdentity) actor() string {
	if i == nil {
		return ""
	}
	return i.UserID
}

// RequireScope authenticates a versioned API request. It accepts a principal
// populated by auth.RequireAPIKey/auth.RequireAuth, or authenticates a bearer
// API key directly. Cookie authentication must be established by the auth
// middleware so its CSRF protections remain in the request chain.
func (h *APIHandler) RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := h.authenticateAPIRequest(r, scope)
			if err != nil {
				status := http.StatusInternalServerError
				switch {
				case errors.Is(err, auth.ErrUnauthorized), errors.Is(err, mongo.ErrNoDocuments):
					status = http.StatusUnauthorized
				case errors.Is(err, auth.ErrForbidden), errors.Is(err, database.ErrForbidden):
					status = http.StatusForbidden
				}
				if status == http.StatusUnauthorized {
					w.Header().Set("WWW-Authenticate", `Bearer realm="flipbook"`)
				}
				jsonError(w, http.StatusText(status), status)
				return
			}
			ctx := context.WithValue(r.Context(), apiIdentityContextKey{}, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (h *APIHandler) authenticateAPIRequest(r *http.Request, scope string) (*apiIdentity, error) {
	minimumRole := apiScopeRole(scope)
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		return h.identityFromPrincipal(r, principal, scope, minimumRole)
	}
	secret, ok := apiBearerToken(r)
	if !ok {
		return nil, auth.ErrUnauthorized
	}
	key, err := h.db.AuthenticateAPIKey(r.Context(), secret, scope)
	if errors.Is(err, database.ErrForbidden) {
		return nil, auth.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	user, err := h.db.GetUser(r.Context(), key.UserID)
	if err != nil || !user.Active() {
		if err != nil {
			return nil, err
		}
		return nil, auth.ErrForbidden
	}
	workspaceID := requestedAPIWorkspace(r, key.WorkspaceID)
	if workspaceID != key.WorkspaceID {
		return nil, auth.ErrForbidden
	}
	membership, err := h.db.GetMembership(r.Context(), workspaceID, user.ID)
	if err != nil {
		return nil, err
	}
	if !key.Active(time.Now().UTC()) || !membership.Role.AtLeast(minimumRole) {
		return nil, auth.ErrForbidden
	}
	return &apiIdentity{UserID: user.ID, WorkspaceID: workspaceID, Role: membership.Role, APIKey: key}, nil
}

func (h *APIHandler) identityFromPrincipal(r *http.Request, principal *auth.Principal, scope string, minimumRole models.Role) (*apiIdentity, error) {
	if principal == nil || principal.User == nil || !principal.User.Active() {
		return nil, auth.ErrUnauthorized
	}
	workspaceID := requestedAPIWorkspace(r, principal.WorkspaceID())
	if principal.APIKey != nil {
		if workspaceID != principal.APIKey.WorkspaceID || !principal.APIKey.HasScope(scope) || !principal.APIKey.Active(time.Now().UTC()) {
			return nil, auth.ErrForbidden
		}
	}
	membership := principal.Membership
	if membership == nil || membership.WorkspaceID != workspaceID {
		var err error
		membership, err = h.db.GetMembership(r.Context(), workspaceID, principal.User.ID)
		if err != nil {
			return nil, err
		}
	}
	if !membership.Role.AtLeast(minimumRole) {
		return nil, auth.ErrForbidden
	}
	return &apiIdentity{UserID: principal.User.ID, WorkspaceID: workspaceID, Role: membership.Role, APIKey: principal.APIKey}, nil
}

func apiBearerToken(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func requestedAPIWorkspace(r *http.Request, fallback string) string {
	for _, candidate := range []string{
		chi.URLParam(r, "workspaceID"),
		strings.TrimSpace(r.Header.Get("X-Workspace-ID")),
		strings.TrimSpace(r.URL.Query().Get("workspace_id")),
	} {
		if candidate != "" {
			return candidate
		}
	}
	if fallback != "" {
		return fallback
	}
	return models.DefaultWorkspaceID
}

func apiScopeRole(scope string) models.Role {
	switch scope {
	case "", models.ScopeFlipbooksRead:
		return models.RoleReader
	case models.ScopeFlipbooksWrite, models.ScopeLeadsWrite:
		return models.RoleEditor
	case models.ScopeIntegrationsWrite, models.ScopeWorkspaceAdmin, "*":
		return models.RoleAdmin
	default:
		return models.RolePublisher
	}
}

// identity returns the scoped identity installed by RequireScope. The legacy
// fallback preserves the original routes, which are already protected by the
// legacy apiAuth middleware in main.go. Versioned routes never use that fallback.
func (h *APIHandler) identity(r *http.Request, required models.Role) (*apiIdentity, error) {
	if identity, ok := r.Context().Value(apiIdentityContextKey{}).(*apiIdentity); ok {
		if !identity.Role.AtLeast(required) {
			return nil, auth.ErrForbidden
		}
		return identity, nil
	}
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		identity, err := h.identityFromPrincipal(r, principal, "", required)
		if err != nil {
			return nil, err
		}
		return identity, nil
	}
	identity := &apiIdentity{UserID: models.LegacyAdminUserID, WorkspaceID: models.DefaultWorkspaceID, Role: models.RoleOwner}
	if !identity.Role.AtLeast(required) {
		return nil, auth.ErrForbidden
	}
	return identity, nil
}

func (h *APIHandler) audit(r *http.Request, identity *apiIdentity, action, resourceType, resourceID string, metadata map[string]string) {
	if identity == nil {
		return
	}
	event := &models.AuditEvent{
		WorkspaceID: identity.WorkspaceID, ActorUserID: identity.UserID,
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Outcome: "success", RequestID: r.Header.Get("X-Request-ID"), Metadata: metadata,
	}
	if identity.APIKey != nil {
		event.ActorAPIKeyID = identity.APIKey.ID
	}
	if err := h.db.RecordAuditEvent(r.Context(), event); err != nil {
		log.Printf("API audit %s failed: %v", action, err)
	}
}

var errAPIInvalid = errors.New("invalid API request")

func apiInvalid(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", errAPIInvalid, err)
}

func apiInvalidError(message string) error { return apiInvalid(errors.New(message)) }

func apiError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	status := http.StatusInternalServerError
	message := "Internal server error"
	switch {
	case errors.Is(err, errAPIInvalid):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, storage.ErrUploadTooLarge):
		status, message = http.StatusRequestEntityTooLarge, "Uploaded file exceeds the configured size limit"
	case errors.Is(err, storage.ErrInvalidUpload), errors.Is(err, storage.ErrInvalidID), errors.Is(err, storage.ErrInvalidStaging):
		status, message = http.StatusBadRequest, "Invalid upload"
	case errors.Is(err, mongo.ErrNoDocuments):
		status, message = http.StatusNotFound, "Resource not found"
	case errors.Is(err, database.ErrConflict), mongo.IsDuplicateKeyError(err):
		status, message = http.StatusConflict, "Resource conflict"
	case errors.Is(err, database.ErrForbidden), errors.Is(err, auth.ErrForbidden):
		status, message = http.StatusForbidden, "Insufficient permissions"
	case errors.Is(err, auth.ErrUnauthorized):
		status, message = http.StatusUnauthorized, "Authentication required"
	case errors.Is(err, database.ErrLastOwner):
		status, message = http.StatusConflict, "Cannot remove the last workspace owner"
	case errors.Is(err, context.Canceled):
		status, message = http.StatusRequestTimeout, "Request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "Operation timed out"
	}
	if status == http.StatusInternalServerError {
		log.Printf("API operation failed: %v", err)
	}
	jsonError(w, message, status)
}

func invalidAPIRequest(w http.ResponseWriter, message string) {
	jsonError(w, message, http.StatusBadRequest)
}

func decodeAPIJSON(r *http.Request, destination any) error {
	if r.Body == nil {
		return errors.New("JSON request body is required")
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, (2<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("only one JSON value is allowed")
		}
		return err
	}
	return nil
}

type apiPagination struct {
	Limit   int64 `json:"limit"`
	Offset  int64 `json:"offset"`
	Total   int64 `json:"total"`
	HasMore bool  `json:"has_more"`
}

func apiPage(r *http.Request, defaultLimit, maximum int64) (int64, int64, error) {
	limit := defaultLimit
	offset := int64(0)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, errors.New("limit must be an integer")
		}
	} else if raw := r.URL.Query().Get("page_size"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, errors.New("page_size must be an integer")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, errors.New("offset must be an integer")
		}
	} else if raw := r.URL.Query().Get("page"); raw != "" {
		page, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || page < 1 {
			return 0, 0, errors.New("page must be a positive integer")
		}
		offset = (page - 1) * limit
	}
	if limit < 1 || limit > maximum {
		return 0, 0, fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	if offset < 0 {
		return 0, 0, errors.New("offset must not be negative")
	}
	return limit, offset, nil
}

func apiTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid RFC3339 time %q", value)
}

func apiStringList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' })
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func normalizeAPITags(tags []string) ([]string, error) {
	if len(tags) > 100 {
		return nil, errors.New("at most 100 tags are allowed")
	}
	result := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || len(tag) > 64 {
			return nil, errors.New("tags must contain 1 to 64 bytes")
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

func randomAPISecret(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validAPIScope(scope string) bool {
	if scope == "*" {
		return true
	}
	switch scope {
	case models.ScopeFlipbooksRead, models.ScopeFlipbooksWrite, models.ScopeAnalyticsRead,
		models.ScopeLeadsRead, models.ScopeLeadsWrite, models.ScopeSalesRead,
		models.ScopeIntegrationsWrite, models.ScopeWorkspaceAdmin:
		return true
	}
	for _, prefix := range []string{"flipbooks:", "analytics:", "leads:", "sales:", "integrations:", "workspace:"} {
		if scope == prefix+"*" {
			return true
		}
	}
	return false
}
