package handlers

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/distribution"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
)

func (h *PublicHandler) publicBaseURL(r *http.Request) string {
	if h.baseURL != "" {
		return h.baseURL
	}
	return distribution.RequestBaseURL("", r)
}

func (h *PublicHandler) Share(w http.ResponseWriter, r *http.Request) {
	fb, _, request, err := h.publication(r, false)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	channel := publishing.ShareChannel(r.URL.Query().Get("channel"))
	if channel == "" {
		channel = publishing.ShareChannel(chi.URLParam(r, "channel"))
	}
	authorization, err := h.publishing.AuthorizeShare(r.Context(), fb.WorkspaceID, fb.ID, channel, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	if channel == publishing.ShareNative || channel == publishing.ShareAny {
		jsonError(w, "a specific share channel is required", http.StatusBadRequest)
		return
	}
	target, err := distribution.ShareURL(channel, distribution.CanonicalViewerURL(h.publicBaseURL(r), fb, 0), fb.Title)
	if err != nil || !strings.HasPrefix(target, "http") && !strings.HasPrefix(target, "mailto:") {
		jsonError(w, "unsupported share channel", http.StatusBadRequest)
		return
	}
	h.recordSimpleEvent(r, authorization, models.AnalyticsShare, 0, 0, map[string]any{"channel": string(channel)})
	http.Redirect(w, r, target, http.StatusSeeOther)
}

type oEmbedResponse struct {
	XMLName         xml.Name `json:"-" xml:"oembed"`
	Version         string   `json:"version" xml:"version"`
	Type            string   `json:"type" xml:"type"`
	Title           string   `json:"title" xml:"title"`
	ProviderName    string   `json:"provider_name" xml:"provider_name"`
	ProviderURL     string   `json:"provider_url" xml:"provider_url"`
	HTML            string   `json:"html" xml:"html"`
	Width           int      `json:"width" xml:"width"`
	Height          int      `json:"height" xml:"height"`
	ThumbnailURL    string   `json:"thumbnail_url,omitempty" xml:"thumbnail_url,omitempty"`
	ThumbnailWidth  int      `json:"thumbnail_width,omitempty" xml:"thumbnail_width,omitempty"`
	ThumbnailHeight int      `json:"thumbnail_height,omitempty" xml:"thumbnail_height,omitempty"`
}

func (h *PublicHandler) OEmbed(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		jsonError(w, "a valid publication url is required", http.StatusBadRequest)
		return
	}
	workspace, err := h.workspaceForPublicationURL(r, parsed)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	slug, err := distribution.SlugFromViewerURL(rawURL, "")
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	fb, err := h.db.GetFlipbookBySlugInWorkspace(r.Context(), workspace.ID, slug)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	request := readerAccessRequest(r, h.publishing, fb.ID, true)
	request.EmbedHost = ""
	authorization, err := h.publishing.AuthorizeEmbed(r.Context(), fb.WorkspaceID, fb.ID, request)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	values := r.URL.Query()
	if values.Get("width") == "" && values.Get("maxwidth") != "" {
		values.Set("width", values.Get("maxwidth"))
	}
	if values.Get("height") == "" && values.Get("maxheight") != "" {
		values.Set("height", values.Get("maxheight"))
	}
	options := distribution.ParseEmbedOptions(values, fb.Embed, fb.PageCount)
	if values.Get("maxwidth") != "" {
		options.Width = readerPositiveInt(values.Get("maxwidth"))
	}
	if values.Get("maxheight") != "" {
		options.Height = readerPositiveInt(values.Get("maxheight"))
	}
	code, err := distribution.EmbedCode(h.publicBaseURL(r), fb, options)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	response := oEmbedResponse{
		Version: "1.0", Type: "rich", Title: fb.Title, ProviderName: workspace.Name,
		ProviderURL: h.publicBaseURL(r) + "/p/" + url.PathEscape(workspace.Slug), HTML: code,
		Width: options.Width, Height: options.Height,
	}
	thumbRequest := request
	thumbRequest.Page = 1
	thumbAuth, thumbErr := h.publishing.AuthorizeAsset(r.Context(), fb.WorkspaceID, fb.ID, publishing.AssetThumbnail, "", thumbRequest)
	if thumbErr == nil && thumbAuth.Revision != nil {
		name := fmt.Sprintf("thumbs/page-"+h.storage.DetectPageFormat(fb.ID)+".png", 1)
		response.ThumbnailURL = distribution.AbsoluteURL(h.publicBaseURL(r), readerAssetURL(fb.ID, thumbAuth.Revision.Number, publishing.AssetThumbnail, 1, name, request.Grant))
		response.ThumbnailWidth = 320
		response.ThumbnailHeight = 240
	}
	applyPublishingHeaders(w, authorization.Decision)
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "", "json":
		jsonResponse(w, response)
	case "xml":
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		body, marshalErr := xml.Marshal(response)
		if marshalErr != nil {
			jsonError(w, "could not encode oEmbed response", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(append([]byte(xml.Header), body...))
	default:
		jsonError(w, "only json and xml oEmbed formats are supported", http.StatusNotImplemented)
	}
}

func (h *PublicHandler) workspaceForPublicationURL(r *http.Request, publicationURL *url.URL) (*models.Workspace, error) {
	base, _ := url.Parse(h.publicBaseURL(r))
	if base != nil && base.Host != "" && strings.EqualFold(base.Host, publicationURL.Host) || strings.EqualFold(r.Host, publicationURL.Host) {
		return readerWorkspace(r, h.db)
	}
	return h.db.GetWorkspaceByDomain(r.Context(), strings.ToLower(publicationURL.Host))
}

func (h *PublicHandler) Robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", publishing.CachePublicReload)
	_, _ = w.Write([]byte(distribution.Robots(h.publicBaseURL(r))))
}

func (h *PublicHandler) Sitemap(w http.ResponseWriter, r *http.Request) {
	workspace, err := readerWorkspace(r, h.db)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	authorizations, err := h.accessiblePublicFlipbooks(r, workspace.ID, database.FlipbookFilter{Sort: "published_at", Descending: true})
	if err != nil {
		writePublishingError(w, err)
		return
	}
	base := h.publicBaseURL(r)
	entries := []distribution.SitemapEntry{{Location: base + "/p/" + url.PathEscape(workspace.Slug), ChangeFreq: "daily", Priority: "0.8"}}
	collections, err := h.db.ListCollections(r.Context(), workspace.ID, models.VisibilityPublic)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	for _, collection := range collections {
		entries = append(entries, distribution.SitemapEntry{Location: base + "/p/" + url.PathEscape(workspace.Slug) + "/collections/" + url.PathEscape(collection.Slug), ModifiedAt: collection.UpdatedAt, ChangeFreq: "weekly", Priority: "0.6"})
	}
	for _, authorization := range authorizations {
		if len(entries) >= 50000 {
			break
		}
		fb := authorization.Flipbook
		if fb.SEO.NoIndex {
			continue
		}
		entries = append(entries, distribution.SitemapEntry{Location: distribution.CanonicalViewerURL(base, &fb, 0), ModifiedAt: fb.UpdatedAt, ChangeFreq: "weekly", Priority: "0.7"})
	}
	body, err := distribution.Sitemap(entries)
	if err != nil {
		jsonError(w, "could not generate sitemap", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", publishing.CachePublicReload)
	_, _ = w.Write(body)
}

func (h *PublicHandler) RSS(w http.ResponseWriter, r *http.Request) {
	workspace, err := readerWorkspace(r, h.db)
	if err != nil {
		writePublishingError(w, err)
		return
	}
	authorizations, err := h.accessiblePublicFlipbooks(r, workspace.ID, database.FlipbookFilter{Sort: "published_at", Descending: true})
	if err != nil {
		writePublishingError(w, err)
		return
	}
	base := h.publicBaseURL(r)
	catalogURL := base + "/p/" + url.PathEscape(workspace.Slug)
	items := make([]distribution.FeedItem, 0, minInt(len(authorizations), 50))
	for i, authorization := range authorizations {
		if i >= 50 {
			break
		}
		fb := authorization.Flipbook
		if fb.SEO.NoIndex {
			continue
		}
		published := fb.CreatedAt
		if fb.PublishedAt != nil {
			published = *fb.PublishedAt
		}
		items = append(items, distribution.FeedItem{Title: fb.Title, Link: distribution.CanonicalViewerURL(base, &fb, 0), Description: fb.Description, GUID: fb.ID, PublishedAt: published})
	}
	body, err := distribution.RSS(workspace.Name, catalogURL, "Latest publications from "+workspace.Name, items)
	if err != nil {
		jsonError(w, "could not generate feed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", publishing.CachePublicReload)
	_, _ = w.Write(body)
}

func (h *PublicHandler) accessiblePublicFlipbooks(r *http.Request, workspaceID string, filter database.FlipbookFilter) ([]*publishing.Authorization, error) {
	filter.WorkspaceID = workspaceID
	filter.Status = models.StatusReady
	filter.PublicationState = models.PublicationPublished
	filter.Visibility = models.VisibilityPublic
	archived := false
	filter.Archived = &archived
	filter.Limit = 500
	result := make([]*publishing.Authorization, 0)
	for offset := int64(0); ; {
		filter.Offset = offset
		items, total, err := h.db.SearchFlipbooks(r.Context(), filter)
		if err != nil {
			return nil, err
		}
		for _, fb := range items {
			authorization, err := h.publishing.AuthorizeViewer(r.Context(), workspaceID, fb.ID, publishing.AccessRequest{})
			if err == nil {
				result = append(result, authorization)
			}
		}
		offset += int64(len(items))
		if len(items) == 0 || offset >= total {
			break
		}
	}
	return result, nil
}

type publicCatalogCard struct {
	ID          string
	Title       string
	Description string
	URL         string
	Thumbnail   string
	PageCount   int
	Language    string
	Tags        []string
}

type publicCatalogViewData struct {
	Workspace     *models.Workspace
	Collection    *models.Collection
	Items         []publicCatalogCard
	Collections   []*models.Collection
	Branding      models.BrandingSettings
	BaseURL       string
	CanonicalURL  string
	RSSURL        string
	MetaTitle     string
	MetaDesc      string
	OGImage       string
	FaviconURL    string
	PrimaryColor  string
	Background    string
	Query         string
	Page          int
	TotalPages    int
	Total         int
	PreviousURL   string
	NextURL       string
	NoIndex       bool
	Language      string
	Direction     string
}

func (h *PublicHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	workspace, err := readerWorkspace(r, h.db)
	if err != nil {
		h.renderCatalogError(w, err)
		return
	}
	filter := database.FlipbookFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Sort: "published_at", Descending: true}
	if tag := strings.TrimSpace(r.URL.Query().Get("tag")); tag != "" {
		filter.Tags = []string{tag}
	}
	authorizations, err := h.accessiblePublicFlipbooks(r, workspace.ID, filter)
	if err != nil {
		h.renderCatalogError(w, err)
		return
	}
	collections, err := h.db.ListCollections(r.Context(), workspace.ID, models.VisibilityPublic)
	if err != nil {
		h.renderCatalogError(w, err)
		return
	}
	data := h.makeCatalogData(r, workspace, nil, authorizations, collections)
	data.Query = filter.Query
	data.PreviousURL, data.NextURL = catalogPaginationURLs(r, data.Page, data.TotalPages)
	applyCatalogHeaders(w, false)
	if err := readerRender(h.tmpl, w, "public_catalog", data, http.StatusOK); err != nil {
		http.Error(w, "catalog template unavailable", http.StatusInternalServerError)
	}
}

func (h *PublicHandler) Collection(w http.ResponseWriter, r *http.Request) {
	workspace, err := readerWorkspace(r, h.db)
	if err != nil {
		h.renderCatalogError(w, err)
		return
	}
	collection, err := h.db.GetCollectionBySlug(r.Context(), workspace.ID, chi.URLParam(r, "collectionSlug"))
	if err != nil {
		h.renderCatalogError(w, err)
		return
	}
	principal := h.catalogPrincipal(r, workspace)
	policyPublication := models.Flipbook{WorkspaceID: workspace.ID, PublicationState: models.PublicationPublished, Status: models.StatusReady, Visibility: collection.Visibility, PageCount: 1}
	decision := publishing.EvaluateAccess(publishing.PolicyInput{Flipbook: policyPublication, Principal: principal, Resource: publishing.ResourceViewer, PageCount: 1, Now: h.options.Now()})
	if !decision.Allowed {
		h.renderCatalogDecision(w, decision)
		return
	}
	authorizations := make([]*publishing.Authorization, 0, len(collection.FlipbookIDs))
	for _, id := range collection.FlipbookIDs {
		fb, getErr := h.db.GetFlipbookInWorkspace(r.Context(), workspace.ID, id)
		if getErr != nil {
			continue
		}
		request := readerAccessRequest(r, h.publishing, id, false)
		authorization, authorizeErr := h.publishing.AuthorizeViewer(r.Context(), workspace.ID, id, request)
		if authorizeErr == nil {
			authorizations = append(authorizations, authorization)
		}
	}
	data := h.makeCatalogData(r, workspace, collection, authorizations, nil)
	data.NoIndex = decision.NoIndex
	data.PreviousURL, data.NextURL = catalogPaginationURLs(r, data.Page, data.TotalPages)
	applyCatalogHeaders(w, decision.NoIndex)
	if err := readerRender(h.tmpl, w, "public_catalog", data, http.StatusOK); err != nil {
		http.Error(w, "collection template unavailable", http.StatusInternalServerError)
	}
}

func (h *PublicHandler) catalogPrincipal(r *http.Request, workspace *models.Workspace) publishing.Principal {
	principal := publishing.Principal{WorkspaceID: workspace.ID}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil {
		return principal
	}
	principal.UserID = user.ID
	if user.ID == workspace.OwnerID {
		principal.Member, principal.Role = true, models.RoleOwner
		return principal
	}
	membership, err := h.db.GetMembership(r.Context(), workspace.ID, user.ID)
	if err == nil {
		principal.Member, principal.Role = true, membership.Role
	}
	return principal
}

func (h *PublicHandler) makeCatalogData(r *http.Request, workspace *models.Workspace, collection *models.Collection, authorizations []*publishing.Authorization, collections []*models.Collection) publicCatalogViewData {
	page := readerPositiveInt(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const pageSize = 24
	total := len(authorizations)
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := minInt(start+pageSize, total)
	cards := make([]publicCatalogCard, 0, maxInt(end-start, 0))
	for _, authorization := range authorizations[start:end] {
		fb := authorization.Flipbook
		card := publicCatalogCard{ID: fb.ID, Title: fb.Title, Description: fb.Description, URL: distribution.CanonicalViewerURL(h.publicBaseURL(r), &fb, 0), PageCount: fb.PageCount, Language: distribution.Language(fb.Language), Tags: fb.Tags}
		request := readerAccessRequest(r, h.publishing, fb.ID, false)
		request.Page = 1
		thumbAuth, err := h.publishing.AuthorizeAsset(r.Context(), fb.WorkspaceID, fb.ID, publishing.AssetThumbnail, "", request)
		if err == nil && thumbAuth.Revision != nil {
			name := fmt.Sprintf("thumbs/page-"+h.storage.DetectPageFormat(fb.ID)+".png", 1)
			card.Thumbnail = readerAssetURL(fb.ID, thumbAuth.Revision.Number, publishing.AssetThumbnail, 1, name, request.Grant)
		}
		cards = append(cards, card)
	}
	base := h.publicBaseURL(r)
	canonical := base + "/p/" + url.PathEscape(workspace.Slug)
	title := workspace.Name
	description := "Browse publications from " + workspace.Name
	if collection != nil {
		canonical += "/collections/" + url.PathEscape(collection.Slug)
		title = collection.Name + " — " + workspace.Name
		if collection.Description != "" {
			description = collection.Description
		}
		if collection.SEO.Title != "" {
			title = collection.SEO.Title
		}
		if collection.SEO.Description != "" {
			description = collection.SEO.Description
		}
	}
	branding := mergeReaderBranding(workspace.Settings.Branding, models.BrandingSettings{})
	favicon := distribution.SafeImageURL(branding.FaviconURL)
	if favicon == "" {
		favicon = "/static/img/favicon.svg"
	}
	ogImage := ""
	if len(cards) > 0 {
		ogImage = distribution.AbsoluteURL(base, cards[0].Thumbnail)
	}
	return publicCatalogViewData{
		Workspace: workspace, Collection: collection, Items: cards, Collections: collections,
		Branding: branding, BaseURL: base, CanonicalURL: canonical, RSSURL: base + "/p/" + url.PathEscape(workspace.Slug) + "/feed.xml",
		MetaTitle: title, MetaDesc: description, OGImage: ogImage, FaviconURL: favicon,
		PrimaryColor: distribution.SafeColor(branding.PrimaryColor, "#6366f1"), Background: distribution.SafeColor(branding.BackgroundColor, "#f8fafc"),
		Page: page, TotalPages: totalPages, Total: total, Language: "en", Direction: "ltr",
	}
}

func catalogPaginationURLs(r *http.Request, page, totalPages int) (string, string) {
	makeURL := func(target int) string {
		if target < 1 || target > totalPages || target == page {
			return ""
		}
		parsed := *r.URL
		query := parsed.Query()
		if target <= 1 {
			query.Del("page")
		} else {
			query.Set("page", strconv.Itoa(target))
		}
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return makeURL(page - 1), makeURL(page + 1)
}

func applyCatalogHeaders(w http.ResponseWriter, noIndex bool) {
	w.Header().Set("Cache-Control", publishing.CachePublicReload)
	if noIndex {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		w.Header().Set("Cache-Control", publishing.CachePrivate)
	}
}

func (h *PublicHandler) renderCatalogError(w http.ResponseWriter, err error) {
	status := publishing.HTTPStatus(err)
	code := publishing.ErrorCodeOf(err)
	message := publishing.PublicMessage(err)
	if errors.Is(err, database.ErrNotFound) {
		status, code, message = http.StatusNotFound, publishing.CodeNotFound, "publication catalog not found"
	}
	w.Header().Set("Cache-Control", publishing.CacheNoStore)
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	_ = readerRender(h.tmpl, w, "public_error", map[string]any{"Status": status, "Code": code, "Message": message, "Language": "en", "Direction": "ltr"}, status)
}

func (h *PublicHandler) renderCatalogDecision(w http.ResponseWriter, decision publishing.Decision) {
	status := decision.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	applyPublishingHeaders(w, decision)
	_ = readerRender(h.tmpl, w, "public_error", map[string]any{"Status": status, "Code": decision.Reason, "Message": "This collection is not available.", "Language": "en", "Direction": "ltr"}, status)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
