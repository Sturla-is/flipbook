package distribution

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
)

var (
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?([0-9a-fA-F]{2})?$`)
	videoID      = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
)

func SafeExternalURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return parsed.String()
}

func SafeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.HasPrefix(raw, "/") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
			return ""
		}
		return parsed.String()
	}
	return SafeExternalURL(raw)
}

func SafeColor(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if colorPattern.MatchString(raw) {
		return raw
	}
	return fallback
}

func AbsoluteURL(baseURL, rawPath string) string {
	base, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || base.Scheme == "" || base.Host == "" {
		return rawPath
	}
	reference, err := url.Parse(rawPath)
	if err != nil {
		return rawPath
	}
	return base.ResolveReference(reference).String()
}

func ViewerURL(baseURL, slug string, page int) string {
	raw := strings.TrimRight(baseURL, "/") + "/v/" + url.PathEscape(slug)
	if page > 1 {
		raw += "?page=" + strconv.Itoa(page)
	}
	return raw
}

func CanonicalViewerURL(baseURL string, fb *models.Flipbook, page int) string {
	if fb != nil && fb.SEO.CanonicalURL != "" {
		if canonical := SafeExternalURL(fb.SEO.CanonicalURL); canonical != "" {
			if page > 1 {
				parsed, _ := url.Parse(canonical)
				query := parsed.Query()
				query.Set("page", strconv.Itoa(page))
				parsed.RawQuery = query.Encode()
				return parsed.String()
			}
			return canonical
		}
	}
	if fb == nil {
		return ""
	}
	return ViewerURL(baseURL, fb.Slug, page)
}

func ShareURL(channel publishing.ShareChannel, publicationURL, title string) (string, error) {
	publicationURL = SafeExternalURL(publicationURL)
	if publicationURL == "" {
		return "", errors.New("an absolute http or https publication URL is required")
	}
	encodedURL := url.QueryEscape(publicationURL)
	encodedTitle := url.QueryEscape(title)
	switch channel {
	case publishing.ShareNative:
		return publicationURL, nil
	case publishing.ShareEmail:
		return "mailto:?subject=" + encodedTitle + "&body=" + encodedURL, nil
	case publishing.ShareFacebook:
		return "https://www.facebook.com/sharer/sharer.php?u=" + encodedURL, nil
	case publishing.ShareLinkedIn:
		return "https://www.linkedin.com/sharing/share-offsite/?url=" + encodedURL, nil
	case publishing.ShareX:
		return "https://twitter.com/intent/tweet?url=" + encodedURL + "&text=" + encodedTitle, nil
	default:
		return "", errors.New("unsupported share channel")
	}
}

func VideoEmbedURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if parsed.Path == "/watch" {
			id := parsed.Query().Get("v")
			if videoID.MatchString(id) {
				return "https://www.youtube-nocookie.com/embed/" + id
			}
		}
		if strings.HasPrefix(parsed.Path, "/shorts/") {
			id := strings.TrimPrefix(parsed.Path, "/shorts/")
			if videoID.MatchString(id) {
				return "https://www.youtube-nocookie.com/embed/" + id
			}
		}
	case "youtu.be":
		id := strings.Trim(parsed.Path, "/")
		if videoID.MatchString(id) {
			return "https://www.youtube-nocookie.com/embed/" + id
		}
	case "vimeo.com", "www.vimeo.com":
		id := strings.Trim(parsed.Path, "/")
		if number, err := strconv.Atoi(id); err == nil && number > 0 {
			return "https://player.vimeo.com/video/" + strconv.Itoa(number)
		}
	}
	return ""
}

func RequestBaseURL(configured string, r *http.Request) string {
	if configured != "" {
		return strings.TrimRight(configured, "/")
	}
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func SlugFromViewerURL(raw, baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid publication URL")
	}
	base, err := url.Parse(baseURL)
	if err == nil && base.Host != "" && !strings.EqualFold(parsed.Host, base.Host) {
		return "", errors.New("publication URL is not on this server")
	}
	cleanPath := path.Clean(parsed.EscapedPath())
	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "v" {
			slug, err := url.PathUnescape(parts[i+1])
			if err != nil || models.NormalizeSlug(slug) != slug || slug == "" {
				return "", errors.New("invalid publication slug")
			}
			return slug, nil
		}
	}
	return "", errors.New("URL is not a publication viewer URL")
}

func IframeTitle(fb *models.Flipbook) string {
	if fb == nil || strings.TrimSpace(fb.Title) == "" {
		return "Interactive flipbook"
	}
	return fb.Title + " — interactive flipbook"
}

func EscapeAttribute(value string) string {
	return html.EscapeString(value)
}
