package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/analytics"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type AnalyticsStore interface {
	GetFlipbookContext(ctx context.Context, id string) (*models.Flipbook, error)
	GetFlipbookInWorkspace(ctx context.Context, workspaceID, id string) (*models.Flipbook, error)
	GetWorkspace(ctx context.Context, workspaceID string) (*models.Workspace, error)
	UpdateWorkspaceSettings(ctx context.Context, workspace *models.Workspace) error
	SearchFlipbooks(ctx context.Context, filter database.FlipbookFilter) ([]*models.Flipbook, int64, error)
	GetMembership(ctx context.Context, workspaceID, userID string) (*models.Membership, error)
	ListAnalyticsEvents(ctx context.Context, filter models.AnalyticsFilter, limit int64) ([]*models.AnalyticsEvent, error)
	InsertAnalyticsEvent(ctx context.Context, event *models.AnalyticsEvent, options database.AnalyticsWriteOptions) error
	GetViewCount(flipbookID string) int
}

type AnalyticsAccess interface {
	AuthorizeViewer(ctx context.Context, workspaceID, flipbookID string, request publishing.AccessRequest) (*publishing.Authorization, error)
	AuthorizeEmbed(ctx context.Context, workspaceID, flipbookID string, request publishing.AccessRequest) (*publishing.Authorization, error)
	GrantFromRequest(request *http.Request, flipbookID string) string
}

type AnalyticsHandler struct {
	store         AnalyticsStore
	access        AnalyticsAccess
	tmpl          *template.Template
	options       analytics.Options
	secureCookies bool
	now           func() time.Time
	random        io.Reader
}

func NewAnalyticsHandler(store AnalyticsStore, access AnalyticsAccess, tmpl *template.Template, cfg *config.Config) *AnalyticsHandler {
	options := analytics.Options{Enabled: true, Retention: 365 * 24 * time.Hour, ConsentPolicy: analytics.ConsentRequired, IgnoreAdminPreviews: true}
	secureCookies := false
	if cfg != nil {
		options = analytics.Options{
			Enabled: cfg.Analytics.Enabled, Retention: time.Duration(cfg.Analytics.RetentionDays) * 24 * time.Hour,
			ConsentPolicy: cfg.Analytics.ConsentPolicy, AllowCountry: cfg.Analytics.AllowCountry,
			ReaderIDPepper: cfg.Analytics.ReaderIDPepper, IgnoreAdminPreviews: cfg.Analytics.IgnoreAdminPreviews,
		}
		secureCookies = cfg.Security.SecureCookies || cfg.Security.HTTPSMode
	}
	return &AnalyticsHandler{
		store: store, access: access, tmpl: tmpl, options: options, secureCookies: secureCookies,
		now: func() time.Time { return time.Now().UTC() }, random: rand.Reader,
	}
}

// Events implements POST /api/public/flipbooks/{id}/events.
func (h *AnalyticsHandler) Events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var batch analytics.IncomingBatch
	if err := decodeAnalyticsJSON(w, r, &batch, analytics.MaxEventBody); err != nil {
		analyticsJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(batch.Events) == 0 {
		analyticsJSONError(w, "events must contain at least one event", http.StatusBadRequest)
		return
	}
	if len(batch.Events) > analytics.MaxBatchEvents {
		analyticsJSONError(w, fmt.Sprintf("at most %d events may be submitted at once", analytics.MaxBatchEvents), http.StatusRequestEntityTooLarge)
		return
	}
	placement, maxPage, err := analyticsBatchShape(batch.Events)
	if err != nil {
		analyticsJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	embedHost := ""
	if len(batch.Events) > 0 {
		embedHost = strings.TrimSpace(batch.Events[0].Metadata["embed_host"])
	}
	fb, authorization, err := h.authorizePublic(r, placement, maxPage, embedHost)
	if err != nil {
		writeAnalyticsAccessError(w, err)
		return
	}
	if err := analyticsCapabilities(batch.Events, authorization); err != nil {
		writeAnalyticsAccessError(w, err)
		return
	}
	options, _, err := h.workspaceOptions(r.Context(), fb.WorkspaceID)
	if err != nil {
		analyticsJSONError(w, "analytics configuration is unavailable", http.StatusServiceUnavailable)
		return
	}
	context := h.requestContext(r, authorization)
	decision := analytics.DecideCollection(options, context)
	if !decision.Collect {
		if context.OptedOut || context.DoNotTrack {
			h.clearReaderCookies(w)
		}
		analyticsJSONResponse(w, map[string]any{"accepted": 0, "ignored": len(batch.Events), "reason": decision.Reason})
		return
	}
	context, err = h.prepareReaderCookies(w, r, options, context, batch.Events)
	if err != nil {
		analyticsJSONError(w, "could not initialize the analytics session", http.StatusServiceUnavailable)
		return
	}
	events := make([]*models.AnalyticsEvent, 0, len(batch.Events))
	for index, incoming := range batch.Events {
		event, buildErr := analytics.BuildEvent(incoming, context, analytics.EnrichOptions{
			Options: options, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
			RevisionID: fb.CurrentRevisionID, PageCount: fb.PageCount, Query: r.URL.Query(), Now: h.now(),
		})
		if buildErr != nil {
			analyticsJSONError(w, fmt.Sprintf("invalid event at index %d: %v", index, buildErr), http.StatusBadRequest)
			return
		}
		events = append(events, event)
	}
	accepted, duplicates := 0, 0
	for _, event := range events {
		err := h.store.InsertAnalyticsEvent(r.Context(), event, database.AnalyticsWriteOptions{
			Retention: options.Retention, ConsentPolicy: options.ConsentPolicy, Consented: context.Consented,
		})
		if mongo.IsDuplicateKeyError(err) && event.ID != "" {
			duplicates++
			continue
		}
		if err != nil {
			analyticsJSONResponseStatus(w, http.StatusServiceUnavailable, map[string]any{
				"error": "could not store events", "accepted": accepted, "duplicates": duplicates,
				"failed": len(events) - accepted, "retryable": true,
			})
			return
		}
		accepted++
	}
	analyticsJSONResponseStatus(w, http.StatusAccepted, map[string]any{"accepted": accepted, "duplicates": duplicates})
}

// Privacy describes the public collection policy and the fields a reader can
// control. It does not expose any stored event data.
func (h *AnalyticsHandler) Privacy(w http.ResponseWriter, r *http.Request) {
	fb, _, err := h.authorizePublic(r, analyticsPlacement(r.URL.Query().Get("placement")), 0)
	if err != nil {
		writeAnalyticsAccessError(w, err)
		return
	}
	options, _, err := h.workspaceOptions(r.Context(), fb.WorkspaceID)
	if err != nil {
		analyticsJSONError(w, "analytics configuration is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	analyticsJSONResponse(w, h.privacyResponse(r, options))
}

// UpdateConsent implements reader opt-in and opt-out. Opting out clears both
// pseudonymous cookies and suppresses all future event collection, including
// aggregate-only events.
func (h *AnalyticsHandler) UpdateConsent(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Consent *bool `json:"consent"`
	}
	if err := decodeAnalyticsJSON(w, r, &input, 4096); err != nil || input.Consent == nil {
		analyticsJSONError(w, "consent must be a boolean", http.StatusBadRequest)
		return
	}
	fb, _, err := h.authorizePublic(r, analyticsPlacement(r.URL.Query().Get("placement")), 0)
	if err != nil {
		writeAnalyticsAccessError(w, err)
		return
	}
	options, _, err := h.workspaceOptions(r.Context(), fb.WorkspaceID)
	if err != nil {
		analyticsJSONError(w, "analytics configuration is unavailable", http.StatusServiceUnavailable)
		return
	}
	expires := h.now().Add(365 * 24 * time.Hour)
	if *input.Consent {
		http.SetCookie(w, analytics.Cookie(analytics.ConsentCookieName, "granted", expires, h.secureCookies))
		context := h.requestContext(r, nil)
		context.Consented, context.OptedOut = true, false
		if _, err := h.prepareReaderCookies(w, r, options, context, nil); err != nil {
			analyticsJSONError(w, "could not initialize the analytics session", http.StatusServiceUnavailable)
			return
		}
	} else {
		http.SetCookie(w, analytics.Cookie(analytics.ConsentCookieName, "denied", expires, h.secureCookies))
		h.clearReaderCookies(w)
	}
	responseRequest := r.Clone(r.Context())
	responseRequest.Header = r.Header.Clone()
	responseRequest.Header.Del("Cookie")
	consentValue := "denied"
	if *input.Consent {
		consentValue = "granted"
	}
	responseRequest.AddCookie(&http.Cookie{Name: analytics.ConsentCookieName, Value: consentValue})
	w.Header().Set("Cache-Control", "no-store")
	analyticsJSONResponse(w, h.privacyResponse(responseRequest, options))
}

func (h *AnalyticsHandler) privacyResponse(r *http.Request, options analytics.Options) map[string]any {
	state, consented, optedOut := analytics.ConsentState(r)
	context := analytics.RequestContext{Consented: consented, OptedOut: optedOut, DoNotTrack: r.Header.Get("DNT") == "1"}
	return map[string]any{
		"enabled": options.Enabled, "consent": state, "consent_policy": options.ConsentPolicy,
		"retention_days":  int(options.Retention / (24 * time.Hour)),
		"country_enabled": options.AllowCountry, "do_not_track": context.DoNotTrack,
		"tracking": analytics.DecideCollection(options, analytics.RequestContext{
			UserAgent: r.UserAgent(), Consented: consented, OptedOut: optedOut, DoNotTrack: context.DoNotTrack,
		}).Collect,
		"identifiers_allowed": analytics.IdentifiersAllowed(options, context),
		"privacy":             "IP addresses and referrer query strings are not stored. Session and reader identifiers are keyed hashes. Reader search text is stored only when a search event is sent.",
		"event_types":         []string{"view", "qualified_read", "page_view", "active_time", "furthest_page", "completion", "cta_click", "outbound_click", "download", "search", "share"},
		"metadata_fields":     []string{"event_id", "placement", "embed_host", "target_url", "query", "cta_id", "link_id", "channel", "method", "format", "utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term"},
	}
}

func (h *AnalyticsHandler) authorizePublic(r *http.Request, placement string, page int, suppliedEmbedHost ...string) (*models.Flipbook, *publishing.Authorization, error) {
	fb, err := h.store.GetFlipbookContext(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return nil, nil, err
	}
	request := publishing.AccessRequest{Page: page, ViaEmbed: placement == analytics.PlacementEmbed}
	if h.access != nil {
		request.Grant = strings.TrimSpace(r.URL.Query().Get("access_grant"))
		if request.Grant == "" {
			request.Grant = h.access.GrantFromRequest(r, fb.ID)
		}
	}
	request.ShareToken = strings.TrimSpace(r.URL.Query().Get("share_token"))
	if user, ok := auth.UserFromContext(r.Context()); ok && user != nil {
		request.Reader.UserID = user.ID
		email := user.NormalizedEmail
		if email == "" {
			email = strings.ToLower(strings.TrimSpace(user.Email))
		}
		if email != "" {
			request.Reader.EmailHash = models.HashToken(email)
		}
	}
	if placement == analytics.PlacementEmbed {
		request.EmbedHost = analyticsEmbedHost(r)
		if len(suppliedEmbedHost) > 0 && suppliedEmbedHost[0] != "" {
			request.EmbedHost = suppliedEmbedHost[0]
		}
	}
	if h.access != nil {
		var authorization *publishing.Authorization
		if request.ViaEmbed {
			authorization, err = h.access.AuthorizeEmbed(r.Context(), fb.WorkspaceID, fb.ID, request)
		} else {
			authorization, err = h.access.AuthorizeViewer(r.Context(), fb.WorkspaceID, fb.ID, request)
		}
		return fb, authorization, err
	}
	authorization, err := h.fallbackAuthorization(r, fb, request)
	return fb, authorization, err
}

func (h *AnalyticsHandler) fallbackAuthorization(r *http.Request, fb *models.Flipbook, request publishing.AccessRequest) (*publishing.Authorization, error) {
	staff := analyticsStaff(r, fb)
	live := fb.IsPublished(h.now())
	if !live && !staff {
		return nil, &publishing.HTTPError{Status: http.StatusNotFound, Code: publishing.CodeNotFound, Message: "publication not found"}
	}
	if !live && fb.Status != models.StatusReady {
		return nil, &publishing.HTTPError{Status: http.StatusConflict, Code: publishing.CodeNotReady, Message: "publication is not ready"}
	}
	if !staff && !fb.AllowsAnonymousAccess(h.now()) {
		return nil, &publishing.HTTPError{Status: http.StatusUnauthorized, Code: publishing.CodeUnauthorized, Message: "authentication required"}
	}
	if !staff && request.ViaEmbed && !fb.Embed.Enabled {
		return nil, &publishing.HTTPError{Status: http.StatusForbidden, Code: publishing.CodeEmbedDisabled, Message: "embedding is disabled"}
	}
	if !staff && request.ViaEmbed && len(fb.Embed.AllowedDomains) > 0 {
		return nil, &publishing.HTTPError{Status: http.StatusServiceUnavailable, Code: publishing.CodeInternal, Message: "publication access is not configured"}
	}
	decision := publishing.Decision{Allowed: true, Status: http.StatusOK, FullAccess: true, PreviewPages: fb.PageCount, Preview: !live}
	if !staff {
		preview := fb.PageCount
		if fb.LeadGate.Enabled && fb.LeadGate.GateReading && (fb.LeadGate.Mandatory || fb.LeadGate.Dismissible) {
			decision.FullAccess = false
			preview = min(preview, fb.LeadGate.PreviewPages)
			decision.LeadGateRequired = true
		}
		if fb.Sales.Enabled {
			decision.FullAccess = false
			preview = min(preview, fb.Sales.PreviewPages)
			decision.PurchaseRequired = true
		}
		decision.PreviewPages = preview
		if request.Page > preview {
			code, status, message := publishing.CodeForbidden, http.StatusForbidden, "access denied"
			if decision.PurchaseRequired {
				code, status, message = publishing.CodePurchaseRequired, http.StatusPaymentRequired, "purchase required"
			} else if decision.LeadGateRequired {
				code, message = publishing.CodeLeadRequired, "lead submission required"
			}
			return nil, &publishing.HTTPError{Status: status, Code: code, Message: message}
		}
	}
	decision.Capabilities = publishing.Capabilities{
		Share:            staff || fb.Share.Enabled,
		DownloadOriginal: staff || fb.Download.Enabled && fb.Download.AllowOriginal,
		DownloadPDF:      staff || fb.Download.Enabled && fb.Download.AllowPDF,
		Print:            staff || fb.Download.Enabled && fb.Download.AllowPrint,
	}
	return &publishing.Authorization{Flipbook: *fb, Decision: decision}, nil
}

func (h *AnalyticsHandler) requestContext(r *http.Request, authorization *publishing.Authorization) analytics.RequestContext {
	_, consented, optedOut := analytics.ConsentState(r)
	context := analytics.RequestContext{
		Referrer: r.Referer(), UserAgent: r.UserAgent(), Country: analytics.RequestCountry(r, h.options.AllowCountry),
		Consented: consented, OptedOut: optedOut, DoNotTrack: r.Header.Get("DNT") == "1",
	}
	if cookie, err := r.Cookie(analytics.SessionCookieName); err == nil && analytics.ValidIdentifier(cookie.Value) {
		context.SessionID = cookie.Value
	}
	if cookie, err := r.Cookie(analytics.ReaderCookieName); err == nil && analytics.ValidIdentifier(cookie.Value) {
		context.ReaderID = cookie.Value
	}
	if authorization != nil && authorization.Decision.Preview {
		if _, ok := auth.UserFromContext(r.Context()); ok {
			context.AdminPreview = true
		}
	}
	return context
}

func (h *AnalyticsHandler) prepareReaderCookies(w http.ResponseWriter, r *http.Request, options analytics.Options, context analytics.RequestContext, events []analytics.IncomingEvent) (analytics.RequestContext, error) {
	if !options.Enabled || !analytics.IdentifiersAllowed(options, context) {
		context.SessionID, context.ReaderID = "", ""
		return context, nil
	}
	for _, event := range events {
		if analytics.ValidIdentifier(event.SessionID) {
			context.SessionID = event.SessionID
			break
		}
	}
	var err error
	if !analytics.ValidIdentifier(context.SessionID) {
		context.SessionID, err = analyticsRandomToken(h.random)
		if err != nil {
			return context, err
		}
	}
	if !analytics.ValidIdentifier(context.ReaderID) {
		context.ReaderID, err = analyticsRandomToken(h.random)
		if err != nil {
			return context, err
		}
	}
	http.SetCookie(w, analytics.Cookie(analytics.SessionCookieName, context.SessionID, h.now().Add(24*time.Hour), h.secureCookies))
	http.SetCookie(w, analytics.Cookie(analytics.ReaderCookieName, context.ReaderID, h.now().Add(365*24*time.Hour), h.secureCookies))
	return context, nil
}

func (h *AnalyticsHandler) clearReaderCookies(w http.ResponseWriter) {
	http.SetCookie(w, analytics.ClearCookie(analytics.SessionCookieName, h.secureCookies))
	http.SetCookie(w, analytics.ClearCookie(analytics.ReaderCookieName, h.secureCookies))
}

func (h *AnalyticsHandler) workspaceOptions(ctx context.Context, workspaceID string) (analytics.Options, *models.Workspace, error) {
	options := h.options
	workspace, err := h.store.GetWorkspace(ctx, workspaceID)
	if errors.Is(err, database.ErrNotFound) {
		return options, nil, nil
	}
	if err != nil {
		return options, nil, err
	}
	if workspace.Settings.AnalyticsConsentPolicy != "" {
		options.ConsentPolicy = workspace.Settings.AnalyticsConsentPolicy
	}
	if workspace.Settings.AnalyticsRetentionDays > 0 {
		options.Retention = time.Duration(workspace.Settings.AnalyticsRetentionDays) * 24 * time.Hour
	}
	return options, workspace, nil
}

func analyticsBatchShape(events []analytics.IncomingEvent) (placement string, maxPage int, err error) {
	placement = analytics.PlacementStandalone
	for index, event := range events {
		current := analytics.PlacementStandalone
		if strings.EqualFold(strings.TrimSpace(event.Metadata["placement"]), analytics.PlacementEmbed) {
			current = analytics.PlacementEmbed
		}
		if index == 0 {
			placement = current
		} else if current != placement {
			return "", 0, errors.New("a batch cannot mix standalone and embed placements")
		}
		if event.Page > maxPage {
			maxPage = event.Page
		}
	}
	return placement, maxPage, nil
}

func analyticsCapabilities(events []analytics.IncomingEvent, authorization *publishing.Authorization) error {
	if authorization == nil {
		return &publishing.HTTPError{Status: http.StatusServiceUnavailable, Code: publishing.CodeInternal, Message: "publication access is unavailable"}
	}
	capabilities := authorization.Decision.Capabilities
	for _, event := range events {
		eventType, ok := analytics.NormalizeEventType(event.Type)
		if !ok {
			continue
		}
		switch eventType {
		case models.AnalyticsDownload:
			if !capabilities.DownloadOriginal && !capabilities.DownloadPDF && !capabilities.Print {
				return &publishing.HTTPError{Status: http.StatusForbidden, Code: publishing.CodeDownloadDisabled, Message: "downloading is disabled"}
			}
		case models.AnalyticsShare:
			if !capabilities.Share {
				return &publishing.HTTPError{Status: http.StatusForbidden, Code: publishing.CodeShareDisabled, Message: "sharing is disabled"}
			}
		}
	}
	return nil
}

func analyticsPlacement(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), analytics.PlacementEmbed) {
		return analytics.PlacementEmbed
	}
	return analytics.PlacementStandalone
}

func analyticsEmbedHost(r *http.Request) string {
	for _, raw := range []string{r.Header.Get("X-Embed-Host"), r.Referer()} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		parsed, err := url.Parse(raw)
		if err == nil && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return strings.ToLower(parsed.Hostname())
		}
	}
	return ""
}

func analyticsStaff(r *http.Request, fb *models.Flipbook) bool {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.User == nil {
		return false
	}
	if principal.User.ID == fb.OwnerID {
		return true
	}
	return principal.WorkspaceID() == fb.WorkspaceID && principal.Role().CanEdit()
}

func analyticsRandomToken(reader io.Reader) (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func decodeAnalyticsJSON(w http.ResponseWriter, r *http.Request, target any, maximum int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maximum)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid JSON request")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeAnalyticsAccessError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	var publishingError *publishing.HTTPError
	if errors.As(err, &publishingError) {
		analyticsJSONError(w, publishing.PublicMessage(err), publishing.HTTPStatus(err))
		return
	}
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
		analyticsJSONError(w, "publication not found", http.StatusNotFound)
		return
	}
	analyticsJSONError(w, "publication access is unavailable", http.StatusServiceUnavailable)
}

func analyticsJSONResponseStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func analyticsJSONResponse(w http.ResponseWriter, value any) {
	analyticsJSONResponseStatus(w, http.StatusOK, value)
}

func analyticsJSONError(w http.ResponseWriter, message string, status int) {
	analyticsJSONResponseStatus(w, status, map[string]string{"error": message})
}
