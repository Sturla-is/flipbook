package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/publishing"
	"golang.org/x/crypto/bcrypt"
)

type adminCollectionChoice struct {
	ID       string
	Name     string
	Selected bool
}

type adminJobView struct {
	ID              string           `json:"id"`
	Type            models.JobType   `json:"type"`
	Status          models.JobStatus `json:"status"`
	Progress        int              `json:"progress"`
	ProgressMessage string           `json:"progress_message,omitempty"`
	Attempts        int              `json:"attempts"`
	MaxAttempts     int              `json:"max_attempts"`
	CancelRequested bool             `json:"cancel_requested"`
	LastError       string           `json:"last_error,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

func (h *AdminHandler) Detail(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleReader)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderDetail(w, r, actor, chi.URLParam(r, "id"), http.StatusOK, nil)
}

func (h *AdminHandler) renderDetail(w http.ResponseWriter, r *http.Request, actor adminActor, id string, status int, overrides map[string]any) {
	data, err := h.detailData(r, actor, id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	for key, value := range overrides {
		data[key] = value
	}
	if err := h.render(w, r, actor, "library", "admin_detail", status, data); err != nil {
		h.renderError(w, r, err)
	}
}

func (h *AdminHandler) detailData(r *http.Request, actor adminActor, id string) (map[string]any, error) {
	flipbook, err := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	workspace, err := h.db.GetWorkspace(r.Context(), actor.WorkspaceID)
	if err != nil {
		return nil, err
	}
	revisions, err := h.db.ListRevisions(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	jobs, err := h.db.ListJobs(r.Context(), database.JobFilter{WorkspaceID: actor.WorkspaceID, FlipbookID: id, Limit: 25})
	if err != nil {
		return nil, err
	}
	collections, err := h.db.ListCollections(r.Context(), actor.WorkspaceID, "")
	if err != nil {
		return nil, err
	}
	selectedCollections := make(map[string]bool, len(flipbook.CollectionIDs))
	for _, collectionID := range flipbook.CollectionIDs {
		selectedCollections[collectionID] = true
	}
	choices := make([]adminCollectionChoice, 0, len(collections))
	for _, collection := range collections {
		choices = append(choices, adminCollectionChoice{ID: collection.ID, Name: collection.Name, Selected: selectedCollections[collection.ID]})
	}
	jobViews := make([]adminJobView, 0, len(jobs))
	for _, job := range jobs {
		jobViews = append(jobViews, adminJobView{
			ID: job.ID, Type: job.Type, Status: job.Status, Progress: job.Progress,
			ProgressMessage: job.ProgressMessage, Attempts: job.Attempts, MaxAttempts: job.MaxAttempts,
			CancelRequested: job.CancelRequested, LastError: job.LastError, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		})
	}
	format := h.storage.DetectPageFormat(id)
	thumbCount := flipbook.PageCount
	if thumbCount > 24 {
		thumbCount = 24
	}
	thumbs := make([]string, 0, thumbCount)
	if flipbook.Status == models.StatusReady {
		for page := 1; page <= thumbCount; page++ {
			thumbs = append(thumbs, h.storage.ThumbImageURL(id, format, page))
		}
	}
	price := ""
	if flipbook.Sales.PriceCents > 0 {
		price = strconv.FormatInt(flipbook.Sales.PriceCents/100, 10) + "." + fmt.Sprintf("%02d", flipbook.Sales.PriceCents%100)
	}
	data := map[string]any{
		"PageTitle":           flipbook.Title + " - Flipbook admin",
		"Flipbook":            flipbook,
		"WorkspaceSettings":   workspace.Settings,
		"Revisions":           revisions,
		"Jobs":                jobViews,
		"Thumbs":              thumbs,
		"ThumbsTruncated":     flipbook.PageCount > len(thumbs),
		"Views":               h.db.GetViewCount(id),
		"BaseURL":             h.baseURL,
		"ViewerURL":           h.baseURL + "/v/" + flipbook.Slug,
		"EmbedCode":           embedCode(h.baseURL, flipbook.Slug),
		"CollectionChoices":   choices,
		"TagString":           joinAdminValues(flipbook.Tags),
		"KeywordString":       joinAdminValues(flipbook.SEO.Keywords),
		"AllowedDomainString": joinAdminValues(flipbook.Embed.AllowedDomains),
		"Price":               price,
		"PublishAtInput":      adminTimeInput(flipbook.PublishAt),
		"ExpiresAtInput":      adminTimeInput(flipbook.ExpiresAt),
		"RequiresReview":      workspace.Settings.RequirePublicationReview,
	}
	return data, nil
}

func adminTimeInput(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format("2006-01-02T15:04")
}

func (h *AdminHandler) Settings(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": "The settings form is invalid or too large."})
		return
	}
	flipbook, err := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	originalCollections := cloneAdminStrings(flipbook.CollectionIDs)
	if err := applyAdminSettings(flipbook, r); err != nil {
		h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	desiredCollections := uniqueAdminStrings(r.Form["collection_ids"])
	for _, collectionID := range desiredCollections {
		if _, err := h.db.GetCollection(r.Context(), actor.WorkspaceID, collectionID); err != nil {
			h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": "One or more selected collections are invalid."})
			return
		}
	}
	flipbook.CollectionIDs = originalCollections
	if err := h.db.UpdateFlipbookMetadata(r.Context(), flipbook); err != nil {
		h.renderError(w, r, err)
		return
	}
	if err := h.syncFlipbookCollections(r, actor.WorkspaceID, id, originalCollections, desiredCollections); err != nil {
		h.audit(r, actor, "publication.settings.update", "flipbook", id, "partial", nil)
		h.renderDetail(w, r, actor, id, http.StatusInternalServerError, map[string]any{
			"Error": "Settings were saved, but collection membership could not be fully updated. Review the collections and retry.",
		})
		return
	}
	h.audit(r, actor, "publication.settings.update", "flipbook", id, "success", nil)
	http.Redirect(w, r, "/admin/flipbooks/"+id+"?notice="+url.QueryEscape("Publication settings saved."), http.StatusSeeOther)
}

func applyAdminSettings(flipbook *models.Flipbook, r *http.Request) error {
	flipbook.Title = strings.TrimSpace(r.FormValue("title"))
	flipbook.Description = strings.TrimSpace(r.FormValue("description"))
	if flipbook.Title == "" || len(flipbook.Title) > 200 {
		return adminInputf("Title is required and must be at most 200 characters.")
	}
	if len(flipbook.Description) > 10000 {
		return adminInputf("Description must be at most 10,000 characters.")
	}
	var err error
	if flipbook.Language, err = validateAdminLanguage(r.FormValue("language")); err != nil {
		return err
	}
	if flipbook.Tags, err = normalizedAdminTags(r.FormValue("tags")); err != nil {
		return err
	}
	flipbook.ExternalSource = strings.TrimSpace(r.FormValue("external_source"))
	flipbook.ExternalID = strings.TrimSpace(r.FormValue("external_id"))
	if len(flipbook.ExternalSource) > 100 || len(flipbook.ExternalID) > 300 {
		return adminInputf("External source or ID is too long.")
	}
	visibility := models.Visibility(r.FormValue("visibility"))
	switch visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted, models.VisibilityPrivate, models.VisibilityTeam, models.VisibilityPassword, models.VisibilityToken:
		flipbook.Visibility = visibility
	default:
		return adminInputf("Choose a valid publication visibility.")
	}
	if password := r.FormValue("password"); password != "" {
		if len(password) < 12 || len(password) > 72 {
			return adminInputf("Publication passwords must contain 12 to 72 characters.")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return err
		}
		flipbook.PasswordHash = string(hash)
	} else if adminFormBool(r, "clear_password") {
		flipbook.PasswordHash = ""
	}
	if flipbook.Visibility == models.VisibilityPassword && flipbook.PasswordHash == "" {
		return adminInputf("Set a publication password before choosing password visibility.")
	}

	flipbook.SEO.Title = strings.TrimSpace(r.FormValue("seo_title"))
	flipbook.SEO.Description = strings.TrimSpace(r.FormValue("seo_description"))
	flipbook.SEO.SocialTitle = strings.TrimSpace(r.FormValue("social_title"))
	flipbook.SEO.SocialDescription = strings.TrimSpace(r.FormValue("social_description"))
	flipbook.SEO.NoIndex = adminFormBool(r, "seo_noindex")
	if flipbook.SEO.CanonicalURL, err = validateAdminURL(r.FormValue("canonical_url"), "Canonical URL", true); err != nil {
		return err
	}
	if flipbook.SEO.CoverImage, err = validateAdminURL(r.FormValue("cover_image"), "Cover image URL", true); err != nil {
		return err
	}
	if flipbook.SEO.SocialImage, err = validateAdminURL(r.FormValue("social_image"), "Social image URL", true); err != nil {
		return err
	}
	if flipbook.SEO.Keywords, err = normalizedAdminTags(r.FormValue("seo_keywords")); err != nil {
		return err
	}

	if flipbook.Branding.LogoURL, err = validateAdminURL(r.FormValue("logo_url"), "Logo URL", true); err != nil {
		return err
	}
	if flipbook.Branding.FaviconURL, err = validateAdminURL(r.FormValue("favicon_url"), "Favicon URL", true); err != nil {
		return err
	}
	if flipbook.Branding.PrimaryColor, err = validateAdminColor(r.FormValue("primary_color"), "Primary color"); err != nil {
		return err
	}
	if flipbook.Branding.BackgroundColor, err = validateAdminColor(r.FormValue("background_color"), "Background color"); err != nil {
		return err
	}
	flipbook.Branding.Attribution = strings.TrimSpace(r.FormValue("attribution"))
	flipbook.Branding.PublisherName = strings.TrimSpace(r.FormValue("publisher_name"))
	if flipbook.Branding.PublisherURL, err = validateAdminURL(r.FormValue("publisher_url"), "Publisher URL", true); err != nil {
		return err
	}
	flipbook.Branding.HideBranding = adminFormBool(r, "hide_branding")

	flipbook.Download.Enabled = adminFormBool(r, "download_enabled")
	flipbook.Download.AllowOriginal = adminFormBool(r, "download_original")
	flipbook.Download.AllowPDF = adminFormBool(r, "download_pdf")
	flipbook.Download.AllowPrint = adminFormBool(r, "download_print")
	flipbook.Embed.Enabled = adminFormBool(r, "embed_enabled")
	flipbook.Embed.ShowToolbar = adminFormBool(r, "embed_toolbar")
	flipbook.Embed.ShowSearch = adminFormBool(r, "embed_search")
	flipbook.Embed.ShowShare = adminFormBool(r, "embed_share")
	flipbook.Embed.ShowDownload = adminFormBool(r, "embed_download")
	if flipbook.Embed.AllowedDomains, err = validateAdminDomains(r.FormValue("embed_domains")); err != nil {
		return err
	}
	if flipbook.Embed.StartPage, err = parseAdminInt(r.FormValue("embed_start_page"), "Embed start page", 1, 1000000); err != nil {
		return err
	}
	if flipbook.PageCount > 0 && flipbook.Embed.StartPage > flipbook.PageCount {
		return adminInputf("Embed start page cannot exceed the publication page count.")
	}
	flipbook.Embed.ReadingMode = strings.TrimSpace(r.FormValue("embed_reading_mode"))
	switch flipbook.Embed.ReadingMode {
	case "", "flip", "single", "double", "continuous", "html":
	default:
		return adminInputf("Choose a supported embed reading mode.")
	}
	if flipbook.Embed.Background, err = validateAdminColor(r.FormValue("embed_background"), "Embed background"); err != nil {
		return err
	}

	flipbook.Share.Enabled = adminFormBool(r, "share_enabled")
	flipbook.Share.Native = adminFormBool(r, "share_native")
	flipbook.Share.Email = adminFormBool(r, "share_email")
	flipbook.Share.Facebook = adminFormBool(r, "share_facebook")
	flipbook.Share.LinkedIn = adminFormBool(r, "share_linkedin")
	flipbook.Share.X = adminFormBool(r, "share_x")

	flipbook.CTA.Enabled = adminFormBool(r, "cta_enabled")
	flipbook.CTA.Text = strings.TrimSpace(r.FormValue("cta_text"))
	if flipbook.CTA.URL, err = validateAdminURL(r.FormValue("cta_url"), "Call-to-action URL", true); err != nil {
		return err
	}
	flipbook.CTA.Style = strings.TrimSpace(r.FormValue("cta_style"))
	switch flipbook.CTA.Style {
	case "", "primary", "secondary", "outline", "link":
	default:
		return adminInputf("Choose a supported call-to-action style.")
	}
	if flipbook.CTA.StartPage, err = parseAdminInt(r.FormValue("cta_start_page"), "Call-to-action start page", 0, 1000000); err != nil {
		return err
	}
	flipbook.CTA.ShowOnEnd = adminFormBool(r, "cta_show_end")
	if flipbook.CTA.Enabled && (flipbook.CTA.Text == "" || flipbook.CTA.URL == "") {
		return adminInputf("An enabled call to action needs both text and a URL.")
	}
	if flipbook.PageCount > 0 && flipbook.CTA.StartPage > flipbook.PageCount {
		return adminInputf("Call-to-action start page cannot exceed the publication page count.")
	}

	flipbook.LeadGate.Enabled = adminFormBool(r, "lead_enabled")
	flipbook.LeadGate.Mandatory = adminFormBool(r, "lead_mandatory")
	flipbook.LeadGate.Dismissible = adminFormBool(r, "lead_dismissible")
	flipbook.LeadGate.GateReading = adminFormBool(r, "lead_gate_reading")
	flipbook.LeadGate.GateDownloads = adminFormBool(r, "lead_gate_downloads")
	flipbook.LeadGate.ConsentText = strings.TrimSpace(r.FormValue("lead_consent_text"))
	flipbook.LeadGate.SuccessMessage = strings.TrimSpace(r.FormValue("lead_success_message"))
	if flipbook.LeadGate.PreviewPages, err = parseAdminInt(r.FormValue("lead_preview_pages"), "Lead preview pages", 0, 1000000); err != nil {
		return err
	}
	if flipbook.LeadGate.Enabled {
		if !flipbook.LeadGate.Mandatory && !flipbook.LeadGate.Dismissible {
			return adminInputf("A lead gate must be mandatory or dismissible.")
		}
		if !flipbook.LeadGate.GateReading && !flipbook.LeadGate.GateDownloads {
			return adminInputf("Choose whether the lead gate applies to reading, downloads, or both.")
		}
		if flipbook.LeadGate.ConsentText == "" {
			return adminInputf("Provide consent text for the lead form.")
		}
	}

	flipbook.Sales.Enabled = adminFormBool(r, "sales_enabled")
	if flipbook.Sales.PriceCents, err = parseAdminPriceCents(r.FormValue("sales_price")); err != nil {
		return err
	}
	flipbook.Sales.Currency = strings.ToUpper(strings.TrimSpace(r.FormValue("sales_currency")))
	if flipbook.Sales.Currency == "" {
		flipbook.Sales.Currency = "USD"
	}
	if !adminCurrencyPattern.MatchString(flipbook.Sales.Currency) {
		return adminInputf("Currency must be a three-letter code.")
	}
	if flipbook.Sales.PreviewPages, err = parseAdminInt(r.FormValue("sales_preview_pages"), "Sales preview pages", 0, 1000000); err != nil {
		return err
	}
	flipbook.Sales.StripePriceID = strings.TrimSpace(r.FormValue("stripe_price_id"))
	if len(flipbook.Sales.StripePriceID) > 255 {
		return adminInputf("Stripe price ID is too long.")
	}
	if flipbook.Sales.Enabled && flipbook.Sales.PriceCents <= 0 {
		return adminInputf("Paid publications require a price greater than zero.")
	}
	if flipbook.PageCount > 0 {
		if flipbook.LeadGate.PreviewPages > flipbook.PageCount || flipbook.Sales.PreviewPages > flipbook.PageCount {
			return adminInputf("Preview page counts cannot exceed the publication page count.")
		}
	}
	flipbook.ApplyDefaults()
	return nil
}

func (h *AdminHandler) syncFlipbookCollections(r *http.Request, workspaceID, flipbookID string, current, desired []string) error {
	currentSet := make(map[string]bool, len(current))
	desiredSet := make(map[string]bool, len(desired))
	for _, id := range current {
		currentSet[id] = true
	}
	for _, id := range desired {
		desiredSet[id] = true
	}
	for _, id := range current {
		if !desiredSet[id] {
			if err := h.db.RemoveFlipbookFromCollection(r.Context(), workspaceID, id, flipbookID); err != nil {
				return err
			}
		}
	}
	for _, id := range desired {
		if !currentSet[id] {
			if err := h.db.AddFlipbookToCollection(r.Context(), workspaceID, id, flipbookID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *AdminHandler) UpdateSlug(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RoleEditor)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": "The slug form is invalid."})
		return
	}
	slug := models.NormalizeSlug(r.FormValue("slug"))
	if slug == "" || len(slug) > 200 {
		h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": "Slug must be non-empty and at most 200 bytes."})
		return
	}
	if h.publisher != nil {
		_, err = h.publisher.ChangeSlug(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, slug)
	} else {
		err = h.db.ChangeFlipbookSlug(r.Context(), actor.WorkspaceID, id, slug)
	}
	if err != nil {
		status, message := adminErrorMessage(err)
		if status == http.StatusInternalServerError {
			status = http.StatusConflict
			message = "The slug could not be changed. It may already be in use."
		}
		h.renderDetail(w, r, actor, id, status, map[string]any{"Error": message})
		return
	}
	h.audit(r, actor, "publication.slug.change", "flipbook", id, "success", map[string]string{"slug": slug})
	http.Redirect(w, r, "/admin/flipbooks/"+id+"?notice="+url.QueryEscape("Slug updated; the previous public URL will redirect."), http.StatusSeeOther)
}

func (h *AdminHandler) CreateShareToken(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RolePublisher)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.renderDetail(w, r, actor, id, http.StatusServiceUnavailable, map[string]any{"Error": "Share-token service wiring is required."})
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": "The share-token form is invalid."})
		return
	}
	expiresAt, err := parseAdminTime(r.FormValue("expires_at"), "Share-token expiry")
	if err != nil {
		h.renderDetail(w, r, actor, id, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	token, err := h.publisher.CreateShareToken(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, r.FormValue("name"), expiresAt)
	if err != nil {
		status, message := adminErrorMessage(err)
		h.renderDetail(w, r, actor, id, status, map[string]any{"Error": message})
		return
	}
	flipbook, err := h.db.GetFlipbookInWorkspace(r.Context(), actor.WorkspaceID, id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.audit(r, actor, "publication.share_token.create", "flipbook", id, "success", map[string]string{"token_id": token.ID})
	h.renderDetail(w, r, actor, id, http.StatusCreated, map[string]any{
		"Notice":        "Share token created. Copy it now; it will not be shown again.",
		"NewShareToken": token.Token,
		"NewShareURL":   h.baseURL + "/v/" + flipbook.Slug + "?share_token=" + url.QueryEscape(token.Token),
	})
}

func (h *AdminHandler) RevokeShareToken(w http.ResponseWriter, r *http.Request) {
	actor, err := adminActorFromRequest(r, models.RolePublisher)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if h.publisher == nil {
		h.renderDetail(w, r, actor, id, http.StatusServiceUnavailable, map[string]any{"Error": "Share-token service wiring is required."})
		return
	}
	tokenID := chi.URLParam(r, "tokenID")
	if err := h.publisher.RevokeShareToken(r.Context(), publishing.Actor{UserID: actor.UserID}, actor.WorkspaceID, id, tokenID); err != nil {
		status, message := adminErrorMessage(err)
		h.renderDetail(w, r, actor, id, status, map[string]any{"Error": message})
		return
	}
	h.audit(r, actor, "publication.share_token.revoke", "flipbook", id, "success", map[string]string{"token_id": tokenID})
	http.Redirect(w, r, "/admin/flipbooks/"+id+"?notice="+url.QueryEscape("Share token revoked."), http.StatusSeeOther)
}
