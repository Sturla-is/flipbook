package distribution

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/metavert-io/flipbook/internal/models"
)

type EmbedOptions struct {
	Width        int
	Height       int
	Aspect       string
	Responsive   bool
	StartPage    int
	ShowToolbar  bool
	ShowSearch   bool
	ShowShare    bool
	ShowDownload bool
	ShowBranding bool
	ReadingMode  string
	Background   string
}

func DefaultEmbedOptions(settings models.EmbedSettings, pageCount int) EmbedOptions {
	page := settings.StartPage
	if page < 1 {
		page = 1
	}
	if pageCount > 0 && page > pageCount {
		page = pageCount
	}
	mode := normalizeReadingMode(settings.ReadingMode)
	return EmbedOptions{
		Width: 800, Height: 600, Responsive: true, StartPage: page,
		ShowToolbar: settings.ShowToolbar, ShowSearch: settings.ShowSearch,
		ShowShare: settings.ShowShare, ShowDownload: settings.ShowDownload,
		ShowBranding: true, ReadingMode: mode,
		Background: SafeColor(settings.Background, ""),
	}
}

func ParseEmbedOptions(values url.Values, settings models.EmbedSettings, pageCount int) EmbedOptions {
	options := DefaultEmbedOptions(settings, pageCount)
	options.Width = boundedInt(values.Get("width"), options.Width, 200, 3840)
	options.Height = boundedInt(values.Get("height"), options.Height, 150, 2160)
	if values.Has("responsive") {
		options.Responsive = parseBool(values.Get("responsive"), options.Responsive)
	}
	if values.Has("page") || values.Has("start_page") {
		value := values.Get("page")
		if value == "" {
			value = values.Get("start_page")
		}
		options.StartPage = boundedInt(value, options.StartPage, 1, maxInt(pageCount, 1))
	}
	options.ShowToolbar = parseBool(values.Get("toolbar"), options.ShowToolbar)
	options.ShowSearch = parseBool(values.Get("search"), options.ShowSearch)
	options.ShowShare = parseBool(values.Get("share"), options.ShowShare)
	options.ShowDownload = parseBool(values.Get("download"), options.ShowDownload)
	options.ShowBranding = parseBool(values.Get("branding"), options.ShowBranding)
	if values.Has("mode") {
		options.ReadingMode = normalizeReadingMode(values.Get("mode"))
	}
	if values.Has("background") {
		options.Background = SafeColor(values.Get("background"), options.Background)
	}
	if aspect := normalizeAspect(values.Get("aspect")); aspect != "" {
		options.Aspect = aspect
		options.Responsive = true
	}
	return options
}

func (o EmbedOptions) Query() url.Values {
	values := make(url.Values)
	if o.StartPage > 1 {
		values.Set("page", strconv.Itoa(o.StartPage))
	}
	values.Set("toolbar", strconv.FormatBool(o.ShowToolbar))
	values.Set("search", strconv.FormatBool(o.ShowSearch))
	values.Set("share", strconv.FormatBool(o.ShowShare))
	values.Set("download", strconv.FormatBool(o.ShowDownload))
	values.Set("branding", strconv.FormatBool(o.ShowBranding))
	if o.ReadingMode != "" && o.ReadingMode != "single" {
		values.Set("mode", o.ReadingMode)
	}
	if o.Background != "" {
		values.Set("background", o.Background)
	}
	if o.Aspect != "" {
		values.Set("aspect", o.Aspect)
	}
	if !o.Responsive {
		values.Set("responsive", "false")
		values.Set("width", strconv.Itoa(o.Width))
		values.Set("height", strconv.Itoa(o.Height))
	}
	return values
}

func EmbedURL(baseURL, slug string, options EmbedOptions) string {
	raw := strings.TrimRight(baseURL, "/") + "/embed/" + url.PathEscape(slug)
	if query := options.Query().Encode(); query != "" {
		raw += "?" + query
	}
	return raw
}

func EmbedCode(baseURL string, fb *models.Flipbook, options EmbedOptions) (string, error) {
	if fb == nil || fb.Slug == "" {
		return "", errors.New("publication and slug are required")
	}
	embedURL := EmbedURL(baseURL, fb.Slug, options)
	title := EscapeAttribute(IframeTitle(fb))
	source := EscapeAttribute(embedURL)
	if options.Responsive {
		aspect := options.Aspect
		if aspect == "" {
			aspect = fmt.Sprintf("%d/%d", options.Width, options.Height)
		}
		return fmt.Sprintf(`<div class="flipbook-embed" style="position:relative;width:100%%;aspect-ratio:%s"><iframe src="%s" title="%s" style="position:absolute;inset:0;width:100%%;height:100%%;border:0" loading="lazy" allowfullscreen></iframe></div>`, EscapeAttribute(aspect), source, title), nil
	}
	return fmt.Sprintf(`<iframe src="%s" title="%s" width="%d" height="%d" style="border:0" loading="lazy" allowfullscreen></iframe>`, source, title, options.Width, options.Height), nil
}

func normalizeReadingMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "double", "two", "two-page", "book":
		return "double"
	case "continuous", "scroll":
		return "continuous"
	case "text", "accessible":
		return "text"
	default:
		return "single"
	}
}

func normalizeAspect(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 {
		return ""
	}
	width, errW := strconv.ParseFloat(parts[0], 64)
	height, errH := strconv.ParseFloat(parts[1], 64)
	if errW != nil || errH != nil || width <= 0 || height <= 0 || width > 100 || height > 100 {
		return ""
	}
	return strconv.FormatFloat(width, 'f', -1, 64) + "/" + strconv.FormatFloat(height, 'f', -1, 64)
}

func parseBool(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boundedInt(value string, fallback, minimum, maximum int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < minimum {
		return minimum
	}
	if parsed > maximum {
		return maximum
	}
	return parsed
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
