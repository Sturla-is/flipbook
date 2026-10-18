package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/metavert-io/flipbook/internal/auth"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/publishing"
	"github.com/metavert-io/flipbook/internal/storage"
	"github.com/metavert-io/flipbook/internal/worker"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const maxUploadSize = 1 << 30

var (
	errAdminUnauthorized = errors.New("admin authentication required")
	errAdminForbidden    = errors.New("insufficient admin permissions")
	errAdminUnavailable  = errors.New("admin service is not configured")
)

type AdminHandler struct {
	db         *database.DB
	storage    *storage.Storage
	worker     *worker.Worker
	tmpl       *template.Template
	baseURL    string
	publisher  *publishing.Service
	auth       *auth.Auth
	httpClient *http.Client
	maxBytes   int64
	now        func() time.Time
}

type AdminOptions struct {
	Publishing *publishing.Service
	Auth       *auth.Auth
	HTTPClient *http.Client
	MaxBytes   int64
	Now        func() time.Time
}

func NewAdminHandler(db *database.DB, store *storage.Storage, w *worker.Worker, tmpl *template.Template, baseURL string, maxUploadSize int64) *AdminHandler {
	return NewAdminHandlerWithOptions(db, store, w, tmpl, baseURL, AdminOptions{MaxBytes: maxUploadSize})
}

func NewAdminHandlerWithOptions(db *database.DB, store *storage.Storage, w *worker.Worker, tmpl *template.Template, baseURL string, options AdminOptions) *AdminHandler {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 45 * time.Second}
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = maxUploadSize
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &AdminHandler{
		db: db, storage: store, worker: w, tmpl: tmpl, baseURL: strings.TrimRight(baseURL, "/"),
		publisher: options.Publishing, auth: options.Auth, httpClient: options.HTTPClient,
		maxBytes: options.MaxBytes, now: options.Now,
	}
}

type adminActor struct {
	UserID      string
	WorkspaceID string
	Role        models.Role
	APIKeyID    string
}

func adminActorFromRequest(r *http.Request, required models.Role) (adminActor, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.User == nil || !principal.User.Active() {
		return adminActor{}, errAdminUnauthorized
	}
	actor := adminActor{UserID: principal.User.ID, WorkspaceID: principal.WorkspaceID(), Role: principal.Role()}
	if principal.APIKey != nil {
		actor.APIKeyID = principal.APIKey.ID
	}
	if actor.WorkspaceID == "" || !actor.Role.Valid() {
		return adminActor{}, errAdminForbidden
	}
	if required.Valid() && !actor.Role.AtLeast(required) {
		return adminActor{}, errAdminForbidden
	}
	return actor, nil
}

func (h *AdminHandler) baseData(w http.ResponseWriter, r *http.Request, actor adminActor, active string) (map[string]any, error) {
	csrfToken := auth.CSRFTokenFromContext(r.Context())
	if csrfToken == "" && h.auth != nil {
		var err error
		csrfToken, err = h.auth.CSRFToken(w, r)
		if err != nil {
			return nil, err
		}
	}
	data := map[string]any{
		"PageTitle":   "Flipbook admin",
		"ActiveNav":   active,
		"CSRFToken":   csrfToken,
		"WorkspaceID": actor.WorkspaceID,
		"CurrentRole": actor.Role,
		"CanEdit":     actor.Role.CanEdit(),
		"CanPublish":  actor.Role.CanPublish(),
		"CanManage":   actor.Role.CanManage(),
		"IsOwner":     actor.Role == models.RoleOwner,
		"Notice":      boundedFlash(r.URL.Query().Get("notice")),
		"Error":       boundedFlash(r.URL.Query().Get("error")),
	}
	if h.db != nil && actor.WorkspaceID != "" {
		workspace, err := h.db.GetWorkspace(r.Context(), actor.WorkspaceID)
		if err != nil {
			return nil, err
		}
		data["Workspace"] = workspace
	}
	return data, nil
}

func boundedFlash(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}

func (h *AdminHandler) render(w http.ResponseWriter, r *http.Request, actor adminActor, active, name string, status int, data map[string]any) error {
	if h.tmpl == nil {
		return errAdminUnavailable
	}
	base, err := h.baseData(w, r, actor, active)
	if err != nil {
		return err
	}
	for key, value := range data {
		base[key] = value
	}
	var output bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&output, name, base); err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if status != 0 {
		w.WriteHeader(status)
	}
	_, err = w.Write(output.Bytes())
	return err
}

func (h *AdminHandler) renderError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := adminErrorMessage(err)
	if status >= http.StatusInternalServerError {
		log.Printf("Admin request failed: %v", err)
	}
	if adminWantsJSON(r) {
		adminJSONError(w, message, status)
		return
	}
	http.Error(w, message, status)
}

func adminErrorMessage(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	var publishingError *publishing.HTTPError
	if errors.As(err, &publishingError) {
		return publishingError.Status, publishingError.Message
	}
	var inputError *adminInputError
	if errors.As(err, &inputError) {
		return http.StatusBadRequest, inputError.message
	}
	switch {
	case errors.Is(err, errAdminUnauthorized), errors.Is(err, auth.ErrUnauthorized):
		return http.StatusUnauthorized, "Authentication is required."
	case errors.Is(err, errAdminForbidden), errors.Is(err, auth.ErrForbidden), errors.Is(err, database.ErrForbidden):
		return http.StatusForbidden, "You do not have permission to perform this action."
	case errors.Is(err, errAdminUnavailable), errors.Is(err, auth.ErrNotConfigured):
		return http.StatusServiceUnavailable, "This action requires final admin service wiring."
	case errors.Is(err, mongo.ErrNoDocuments):
		return http.StatusNotFound, "The requested resource was not found."
	case errors.Is(err, database.ErrLastOwner):
		return http.StatusConflict, "A workspace must retain at least one owner."
	case errors.Is(err, database.ErrConflict), errors.Is(err, storage.ErrRevisionActive), errors.Is(err, storage.ErrRevisionExists):
		return http.StatusConflict, "The requested change conflicts with the current resource state."
	case errors.Is(err, storage.ErrUploadTooLarge):
		return http.StatusRequestEntityTooLarge, "The uploaded file exceeds the configured size limit."
	case errors.Is(err, storage.ErrInvalidUpload):
		return http.StatusBadRequest, "The uploaded filename or file content is invalid."
	case errors.Is(err, context.Canceled):
		return 499, "The request was cancelled."
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "The operation timed out. Check its status before retrying."
	default:
		return http.StatusInternalServerError, "The operation could not be completed. Try again or review the item status."
	}
}

func adminWantsJSON(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "XMLHttpRequest" || strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (h *AdminHandler) audit(r *http.Request, actor adminActor, action, resourceType, resourceID, outcome string, metadata map[string]string) {
	if h.db == nil {
		return
	}
	if outcome == "" {
		outcome = "success"
	}
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if len(requestID) > 128 {
		requestID = requestID[:128]
	}
	event := &models.AuditEvent{
		WorkspaceID: actor.WorkspaceID, ActorUserID: actor.UserID, ActorAPIKeyID: actor.APIKeyID,
		Action: action, ResourceType: resourceType, ResourceID: resourceID, Outcome: outcome,
		RequestID: requestID, Metadata: metadata, CreatedAt: h.now().UTC(),
	}
	if err := h.db.RecordAuditEvent(r.Context(), event); err != nil {
		log.Printf("Admin audit %s failed: %v", action, err)
	}
}

func adminJSONResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if status != 0 {
		w.WriteHeader(status)
	}
	_ = json.NewEncoder(w).Encode(data)
}

func adminJSONError(w http.ResponseWriter, message string, status int) {
	adminJSONResponse(w, status, map[string]string{"error": message})
}

func embedCode(baseURL, slug string) string {
	return `<iframe src="` + baseURL + `/embed/` + slug + `" width="800" height="600" frameborder="0" allowfullscreen title="Interactive flipbook" style="border:none;border-radius:8px;box-shadow:0 2px 8px rgba(0,0,0,0.1);"></iframe>`
}

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == ' ' {
			return r
		}
		return -1
	}, s)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "flipbook"
	}
	return s
}

func adminDecodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode admin request: %w", err)
	}
	return nil
}
