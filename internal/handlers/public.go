package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/auth"
	"github.com/metavert-io/flipbook/internal/converter"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/distribution"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/publishing"
	"github.com/metavert-io/flipbook/internal/storage"
)

type PublicCheckoutInput struct {
	WorkspaceID string
	FlipbookID  string
	ReturnURL   string
	Reader      publishing.Reader
}

type PublicCheckoutProvider interface {
	CreateCheckout(ctx context.Context, input PublicCheckoutInput) (string, error)
}

type PublicCheckoutFunc func(context.Context, PublicCheckoutInput) (string, error)

func (f PublicCheckoutFunc) CreateCheckout(ctx context.Context, input PublicCheckoutInput) (string, error) {
	return f(ctx, input)
}

type PublicOptions struct {
	Checkout            PublicCheckoutProvider
	AnalyticsDisabled   bool
	AnalyticsRetention  time.Duration
	AnalyticsPolicy     string
	AnalyticsPepper     string
	AllowCountry        bool
	IgnoreAdminPreviews bool
	Now                 func() time.Time
}

type PublicHandler struct {
	db         *database.DB
	storage    *storage.Storage
	tmpl       *template.Template
	baseURL    string
	publishing *publishing.Service
	checkout   PublicCheckoutProvider
	options    PublicOptions
}

func NewPublicHandler(db *database.DB, store *storage.Storage, tmpl *template.Template, baseURL string, service *publishing.Service, options ...PublicOptions) *PublicHandler {
	settings := PublicOptions{AnalyticsPolicy: "required", AnalyticsRetention: 365 * 24 * time.Hour, Now: func() time.Time { return time.Now().UTC() }}
	if len(options) > 0 {
		provided := options[0]
		checkout := provided.Checkout
		if provided.AnalyticsPolicy == "" {
			provided.AnalyticsPolicy = settings.AnalyticsPolicy
		}
		if provided.AnalyticsRetention == 0 {
			provided.AnalyticsRetention = settings.AnalyticsRetention
		}
		if provided.Now == nil {
			provided.Now = settings.Now
		}
		provided.Checkout = checkout
		settings = provided
	}
	return &PublicHandler{db: db, storage: store, tmpl: tmpl, baseURL: strings.TrimRight(baseURL, "/"), publishing: service, checkout: settings.Checkout, options: settings}
}

func (h *PublicHandler) SetCheckoutProvider(provider PublicCheckoutProvider) *PublicHandler {
	h.checkout = provider
	return h
}

func (h *PublicHandler) publication(r *http.Request, viaEmbed bool) (*models.Flipbook, *publishing.Authorization, publishing.AccessRequest, error) {
	if h.publishing == nil {
		return nil, nil, publishing.AccessRequest{}, errors.New("publication access is not configured")
	}
	id := chi.URLParam(r, "id")
	fb, err := h.db.GetFlipbookContext(r.Context(), id)
	if err != nil {
		return nil, nil, publishing.AccessRequest{}, err
	}
	request := readerAccessRequest(r, h.publishing, fb.ID, viaEmbed)
	var authorization *publishing.Authorization
	if viaEmbed {
		authorization, err = h.publishing.AuthorizeEmbed(r.Context(), fb.WorkspaceID, fb.ID, request)
	} else {
		authorization, err = h.publishing.AuthorizeViewer(r.Context(), fb.WorkspaceID, fb.ID, request)
	}
	return fb, authorization, request, err
}

func (h *PublicHandler) Access(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password   string `json:"password"`
		ShareToken string `json:"share_token"`
	}
	if err := decodePublicJSON(w, r, &input, 16<<10); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if (input.Password == "") == (input.ShareToken == "") {
		jsonError(w, "exactly one of password or share_token is required", http.StatusBadRequest)
		return
	}
	fb, err := h.db.GetFlipbookContext(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writePublishingError(w, err)
		return
	}
	request := readerAccessRequest(r, h.publishing, fb.ID, false)
	var grant *publishing.GrantResult
	if input.Password != "" {
		grant, err = h.publishing.IssuePasswordGrant(r.Context(), fb.WorkspaceID, fb.ID, input.Password, request)
	} else {
		grant, err = h.publishing.IssueShareGrant(r.Context(), fb.WorkspaceID, fb.ID, input.ShareToken, request)
	}
	if err != nil {
		writePublishingError(w, err)
		return
	}
	http.SetCookie(w, grant.Cookie)
	jsonResponse(w, map[string]any{"ok": true, "expires_at": grant.ExpiresAt})
}

func (h *PublicHandler) Lead(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Consent bool   `json:"consent"`
	}
	if err := decodePublicJSON(w, r, &input, 16<<10); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	input.Email = strings.TrimSpace(input.Email)
	input.Name = strings.TrimSpace(input.Name)
	address, err := mail.ParseAddress(input.Email)
	if err != nil || !strings.EqualFold(address.Address, input.Email) || len(input.Email) > 320 || len(input.Name) > 200 || !input.Consent {
		jsonError(w, "a valid email and explicit consent are required", http.StatusBadRequest)
		return
	}
	fb, authorization, request, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	if !fb.LeadGate.Enabled {
		jsonError(w, "lead capture is not enabled for this publication", http.StatusBadRequest)
		return
	}
	lead := &models.Lead{
		WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: input.Email, Name: input.Name,
		Consented: true, ConsentText: fb.LeadGate.ConsentText, Source: readerPlacement(false),
		UTM: models.CampaignFromQuery(r.URL.Query()), Metadata: map[string]string{},
	}
	if err := h.db.UpsertLead(r.Context(), lead); err != nil {
		jsonError(w, "could not save lead", http.StatusServiceUnavailable)
		return
	}
	grant, err := h.publishing.IssueLeadGrant(r.Context(), fb.WorkspaceID, fb.ID, true, false, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	http.SetCookie(w, grant.Cookie)
	h.recordSimpleEvent(r, authorization, models.AnalyticsLead, 0, 0, nil)
	message := fb.LeadGate.SuccessMessage
	if message == "" {
		message = "Thank you. You can continue reading."
	}
	jsonResponse(w, map[string]any{"ok": true, "message": message})
}

func (h *PublicHandler) DismissLead(w http.ResponseWriter, r *http.Request) {
	fb, _, request, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	grant, err := h.publishing.IssueLeadGrant(r.Context(), fb.WorkspaceID, fb.ID, false, true, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	http.SetCookie(w, grant.Cookie)
	jsonResponse(w, map[string]any{"ok": true})
}

func (h *PublicHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	fb, authorization, _, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	if !fb.Sales.Enabled || !authorization.Decision.PurchaseRequired {
		jsonError(w, "checkout is not required for this publication", http.StatusConflict)
		return
	}
	if h.checkout == nil {
		jsonError(w, "checkout is not configured", http.StatusServiceUnavailable)
		return
	}
	returnURL := distribution.CanonicalViewerURL(h.publicBaseURL(r), fb, 0)
	checkoutURL, err := h.checkout.CreateCheckout(r.Context(), PublicCheckoutInput{
		WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, ReturnURL: returnURL,
		Reader: readerAccessRequest(r, h.publishing, fb.ID, false).Reader,
	})
	if err != nil {
		jsonError(w, "could not create checkout", http.StatusBadGateway)
		return
	}
	if distribution.SafeExternalURL(checkoutURL) == "" {
		jsonError(w, "checkout provider returned an invalid URL", http.StatusBadGateway)
		return
	}
	jsonResponse(w, map[string]string{"checkout_url": checkoutURL})
}

type publicEventInput struct {
	Type       string         `json:"type"`
	Page       int            `json:"page"`
	DurationMS int64          `json:"duration_ms"`
	SessionID  string         `json:"session_id"`
	Metadata   map[string]any `json:"metadata"`
}

func (h *PublicHandler) Events(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Events []publicEventInput `json:"events"`
	}
	if err := decodePublicJSON(w, r, &input, 128<<10); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(input.Events) == 0 {
		jsonResponse(w, map[string]int{"accepted": 0})
		return
	}
	if len(input.Events) > 50 {
		jsonError(w, "at most 50 events may be submitted at once", http.StatusRequestEntityTooLarge)
		return
	}
	fb, authorization, _, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	if h.options.AnalyticsDisabled || models.IsObviousBot(r.UserAgent()) || h.ignoreAdminPreview(r, authorization) {
		jsonResponse(w, map[string]int{"accepted": 0})
		return
	}
	accepted := 0
	for index, incoming := range input.Events {
		eventType, ok := publicAnalyticsType(incoming.Type)
		if !ok || incoming.Page < 0 || incoming.Page > fb.PageCount || incoming.DurationMS < 0 || incoming.DurationMS > int64(24*time.Hour/time.Millisecond) || len(incoming.SessionID) > 128 {
			jsonError(w, fmt.Sprintf("invalid event at index %d", index), http.StatusBadRequest)
			return
		}
		if incoming.Page > 0 && !authorization.Decision.FullAccess && incoming.Page > authorization.Decision.PreviewPages {
			jsonError(w, fmt.Sprintf("event page is outside the accessible preview at index %d", index), http.StatusForbidden)
			return
		}
		event := h.makeAnalyticsEvent(r, fb, eventType, incoming.Page, incoming.DurationMS, incoming.SessionID, incoming.Metadata)
		if err := h.insertAnalyticsEvent(r, fb.WorkspaceID, event); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "could not store events", "accepted": accepted})
			return
		}
		accepted++
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]int{"accepted": accepted})
}

func publicAnalyticsType(value string) (models.AnalyticsEventType, bool) {
	switch value {
	case "view":
		return models.AnalyticsView, true
	case "qualified_read":
		return models.AnalyticsQualifiedRead, true
	case "page_view", "page_exposure":
		return models.AnalyticsPageExposure, true
	case "active_time":
		return models.AnalyticsActiveTime, true
	case "furthest_page":
		return models.AnalyticsFurthestPage, true
	case "completion":
		return models.AnalyticsCompletion, true
	case "cta_click":
		return models.AnalyticsCTAClick, true
	case "outbound_click":
		return models.AnalyticsOutboundClick, true
	case "download":
		return models.AnalyticsDownload, true
	case "search":
		return models.AnalyticsSearch, true
	case "share":
		return models.AnalyticsShare, true
	case "lead":
		return models.AnalyticsLead, true
	case "purchase":
		return models.AnalyticsPurchase, true
	default:
		return "", false
	}
}

func (h *PublicHandler) makeAnalyticsEvent(r *http.Request, fb *models.Flipbook, eventType models.AnalyticsEventType, page int, durationMS int64, sessionID string, metadata map[string]any) *models.AnalyticsEvent {
	device, browser := models.CoarseDeviceBrowser(r.UserAgent())
	event := &models.AnalyticsEvent{
		WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, RevisionID: fb.CurrentRevisionID,
		Type: eventType, OccurredAt: h.options.Now().UTC(), Page: page,
		ActiveSeconds: durationMS / 1000, Value: durationMS,
		Referrer: models.NormalizeReferrer(r.Referer()), UTM: models.CampaignFromQuery(r.URL.Query()),
		Placement: readerPlacement(strings.Contains(r.URL.Path, "/embed/")), Device: device, Browser: browser,
		Metadata: sanitizePublicMetadata(metadata),
	}
	if placement := r.URL.Query().Get("placement"); placement == "embed" || placement == "standalone" {
		event.Placement = placement
	} else if placement := event.Metadata["placement"]; placement == "embed" || placement == "standalone" {
		event.Placement = placement
	}
	if sessionID != "" {
		mac := hmac.New(sha256.New, []byte(h.options.AnalyticsPepper))
		_, _ = io.WriteString(mac, sessionID)
		event.SessionIDHash = hex.EncodeToString(mac.Sum(nil))
	}
	if h.options.AllowCountry {
		event.Country = r.Header.Get("CF-IPCountry")
		if event.Country == "" {
			event.Country = r.Header.Get("X-Country-Code")
		}
	}
	if query := publicMetadataString(metadata, "query", 200); query != "" {
		event.SearchTerms = query
	}
	if host := publicMetadataString(metadata, "host", 255); host != "" {
		if parsed, err := url.Parse("https://" + host); err == nil && parsed.Hostname() != "" {
			event.TargetHost = strings.ToLower(parsed.Hostname())
		}
	}
	return event
}

func (h *PublicHandler) insertAnalyticsEvent(r *http.Request, workspaceID string, event *models.AnalyticsEvent) error {
	policy := h.options.AnalyticsPolicy
	retention := h.options.AnalyticsRetention
	if workspace, err := h.db.GetWorkspace(r.Context(), workspaceID); err == nil {
		if workspace.Settings.AnalyticsConsentPolicy != "" {
			policy = workspace.Settings.AnalyticsConsentPolicy
		}
		if workspace.Settings.AnalyticsRetentionDays > 0 {
			retention = time.Duration(workspace.Settings.AnalyticsRetentionDays) * 24 * time.Hour
		}
	}
	return h.db.InsertAnalyticsEvent(r.Context(), event, database.AnalyticsWriteOptions{Retention: retention, ConsentPolicy: policy, Consented: readerAnalyticsConsent(r)})
}

func (h *PublicHandler) recordSimpleEvent(r *http.Request, authorization *publishing.Authorization, eventType models.AnalyticsEventType, page int, durationMS int64, metadata map[string]any) {
	if h.options.AnalyticsDisabled || authorization == nil || models.IsObviousBot(r.UserAgent()) || h.ignoreAdminPreview(r, authorization) {
		return
	}
	event := h.makeAnalyticsEvent(r, &authorization.Flipbook, eventType, page, durationMS, "", metadata)
	_ = h.insertAnalyticsEvent(r, authorization.Flipbook.WorkspaceID, event)
}

func (h *PublicHandler) ignoreAdminPreview(r *http.Request, authorization *publishing.Authorization) bool {
	if !h.options.IgnoreAdminPreviews || authorization == nil || !authorization.Decision.Preview {
		return false
	}
	_, ok := auth.UserFromContext(r.Context())
	return ok
}

func sanitizePublicMetadata(input map[string]any) map[string]string {
	output := make(map[string]string)
	if len(input) == 0 {
		return output
	}
	count := 0
	for key, value := range input {
		if count >= 16 {
			break
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if len(key) == 0 || len(key) > 50 || strings.Contains(key, "email") || strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "cookie") || strings.Contains(key, "ip") {
			continue
		}
		var text string
		switch typed := value.(type) {
		case string:
			text = typed
		case float64:
			text = strconv.FormatFloat(typed, 'f', -1, 64)
		case bool:
			text = strconv.FormatBool(typed)
		default:
			continue
		}
		text = strings.TrimSpace(text)
		if len(text) > 500 {
			text = text[:500]
		}
		output[key] = text
		count++
	}
	return output
}

func publicMetadataString(metadata map[string]any, key string, maximum int) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}

func (h *PublicHandler) Asset(w http.ResponseWriter, r *http.Request) {
	fb, _, request, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	kind := publishing.AssetKind(r.URL.Query().Get("kind"))
	request.Page = readerPositiveInt(r.URL.Query().Get("page"))
	request.Revision = readerPositiveInt(r.URL.Query().Get("revision"))
	name := r.URL.Query().Get("name")
	authorization, err := h.publishing.AuthorizeAsset(r.Context(), fb.WorkspaceID, fb.ID, kind, name, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	if authorization.Revision == nil || !h.validAssetName(fb.ID, authorization.Revision.Number, kind, request.Page, authorization.AssetName) {
		jsonError(w, "invalid asset name", http.StatusBadRequest)
		return
	}
	applyPublishingHeaders(w, authorization.Decision)
	if err := readerServeFile(r.Context(), w, r, h.storage, fb.ID, authorization.Revision.Number, authorization.AssetName, "", false); err != nil {
		readerInternalError(w, err)
	}
}

func (h *PublicHandler) validAssetName(flipbookID string, revision int, kind publishing.AssetKind, page int, name string) bool {
	if kind == publishing.AssetManifest || kind == publishing.AssetText {
		return name == "manifest.json" || name == "text.json"
	}
	if page < 1 || name == "" {
		return false
	}
	directory, err := readerArtifactDir(h.storage, flipbookID, revision)
	if err == nil {
		if manifest, loadErr := converter.LoadManifest(directory); loadErr == nil && page <= len(manifest.Pages) {
			for _, rendition := range manifest.Pages[page-1].Images {
				if rendition.Path != name {
					continue
				}
				switch kind {
				case publishing.AssetPage:
					return rendition.Role == "page" && strings.HasPrefix(name, "pages/")
				case publishing.AssetThumbnail:
					return rendition.Role == "thumbnail" && strings.HasPrefix(name, "thumbs/")
				case publishing.AssetRendition:
					return strings.HasPrefix(name, "renditions/")
				}
			}
			return false
		}
	}
	base := filepath.Base(name)
	extension := filepath.Ext(base)
	stem := strings.TrimSuffix(base, extension)
	var prefix string
	switch kind {
	case publishing.AssetPage:
		prefix = "page-"
	case publishing.AssetThumbnail:
		prefix = "page-"
	case publishing.AssetRendition:
		if strings.HasPrefix(stem, "thumb-") {
			prefix = "thumb-"
		} else {
			prefix = "page-"
		}
	default:
		return false
	}
	if !strings.HasPrefix(stem, prefix) {
		return false
	}
	number, err := strconv.Atoi(strings.TrimPrefix(stem, prefix))
	return err == nil && number == page && (extension == ".png" || extension == ".jpg" || extension == ".jpeg" || extension == ".webp")
}

func (h *PublicHandler) Download(w http.ResponseWriter, r *http.Request) {
	fb, _, request, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	kind := publishing.DownloadKind(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = publishing.DownloadPDF
	}
	authorization, err := h.publishing.AuthorizeDownload(r.Context(), fb.WorkspaceID, fb.ID, kind, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	revision, revisionErr := h.db.GetCurrentRevision(r.Context(), fb.WorkspaceID, fb.ID)
	revisionNumber := 0
	if revisionErr == nil {
		revisionNumber = revision.Number
	}
	directory, _ := readerArtifactDir(h.storage, fb.ID, revisionNumber)
	manifest, _ := converter.LoadManifest(directory)
	name := "converted.pdf"
	extension := ".pdf"
	attachment := true
	switch kind {
	case publishing.DownloadOriginal:
		extension = filepath.Ext(fb.Filename)
		if extension == "" {
			extension = ".pdf"
		}
		name = "original" + extension
		if manifest != nil && manifest.Original != "" {
			name = manifest.Original
			extension = filepath.Ext(name)
		}
	case publishing.DownloadPDF:
		if manifest != nil && manifest.SourcePDF != "" {
			name = manifest.SourcePDF
		}
	case publishing.DownloadPrint:
		if manifest != nil && manifest.SourcePDF != "" {
			name = manifest.SourcePDF
		}
		attachment = false
	default:
		jsonError(w, "unsupported download kind", http.StatusBadRequest)
		return
	}
	applyPublishingHeaders(w, authorization.Decision)
	downloadName := readerDownloadName(fb, extension)
	if err := readerServeFile(r.Context(), w, r, h.storage, fb.ID, revisionNumber, name, downloadName, attachment); err != nil {
		readerInternalError(w, err)
		return
	}
	h.recordSimpleEvent(r, authorization, models.AnalyticsDownload, 0, 0, map[string]any{"kind": string(kind)})
}

func (h *PublicHandler) QR(w http.ResponseWriter, r *http.Request) {
	fb, _, request, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	authorization, err := h.publishing.AuthorizeShare(r.Context(), fb.WorkspaceID, fb.ID, publishing.ShareAny, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	page := readerPositiveInt(r.URL.Query().Get("page"))
	if page > fb.PageCount || !authorization.Decision.FullAccess && page > authorization.Decision.PreviewPages {
		jsonError(w, "page is outside the accessible publication", http.StatusBadRequest)
		return
	}
	publicationURL := distribution.CanonicalViewerURL(h.publicBaseURL(r), fb, page)
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "svg"
	}
	if format != "svg" && format != "png" {
		jsonError(w, "only svg and png QR formats are supported", http.StatusBadRequest)
		return
	}
	var content []byte
	if format == "png" {
		size := readerPositiveInt(r.URL.Query().Get("size"))
		content, err = distribution.QRCodePNG(publicationURL, size)
		w.Header().Set("Content-Type", "image/png")
	} else {
		content, err = distribution.QRCodeSVG(publicationURL, 0, 0)
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	}
	if err != nil {
		jsonError(w, "could not generate QR code", http.StatusBadRequest)
		return
	}
	applyPublishingHeaders(w, authorization.Decision)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", readerDownloadName(fb, "."+format)))
	_, _ = w.Write(content)
}

func decodePublicJSON(w http.ResponseWriter, r *http.Request, target any, maximum int64) error {
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
