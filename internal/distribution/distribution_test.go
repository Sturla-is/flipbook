package distribution

import (
	"bytes"
	"image/png"
	"net/url"
	"strings"
	"testing"

	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/publishing"
)

func TestEmbedOptionsAndCode(t *testing.T) {
	fb := &models.Flipbook{Title: `Annual "Report"`, Slug: "annual-report", PageCount: 12, Embed: models.EmbedSettings{StartPage: 3, ShowToolbar: true, ShowSearch: true}}
	values, _ := url.ParseQuery("aspect=16%2F9&page=4&mode=continuous&share=true&background=%23223344")
	options := ParseEmbedOptions(values, fb.Embed, fb.PageCount)
	if options.StartPage != 4 || options.ReadingMode != "continuous" || !options.ShowShare || options.Background != "#223344" {
		t.Fatalf("unexpected options: %+v", options)
	}
	code, err := EmbedCode("https://reader.example", fb, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"aspect-ratio:16/9", "page=4", "mode=continuous", "title=", "allowfullscreen", "loading=\"lazy\""} {
		if !strings.Contains(code, expected) {
			t.Errorf("embed code missing %q: %s", expected, code)
		}
	}
	if strings.Contains(code, "<script") {
		t.Fatal("embed code contains script")
	}
}

func TestEmbedOptionsUseSavedFrameSize(t *testing.T) {
	fb := &models.Flipbook{Title: "Report", Slug: "report", PageCount: 4}
	options := DefaultEmbedOptions(fb.Embed, fb.PageCount)
	if options.Width != 1200 || options.Height != 800 {
		t.Fatalf("unsaved embed size = %dx%d, want 1200x800", options.Width, options.Height)
	}
	code, err := EmbedCode("https://reader.example", fb, options)
	if err != nil || !strings.Contains(code, "aspect-ratio:1200/800") {
		t.Fatalf("default embed code = %q, %v", code, err)
	}

	fb.Embed = models.EmbedSettings{Width: 960, Height: 540}
	options = ParseEmbedOptions(url.Values{}, fb.Embed, fb.PageCount)
	if options.Width != 960 || options.Height != 540 {
		t.Fatalf("saved embed size = %dx%d, want 960x540", options.Width, options.Height)
	}
	options = ParseEmbedOptions(url.Values{"width": {"640"}, "height": {"480"}}, fb.Embed, fb.PageCount)
	if options.Width != 640 || options.Height != 480 {
		t.Fatalf("query size should override the saved size, got %dx%d", options.Width, options.Height)
	}
}

func TestShareAndVideoURLs(t *testing.T) {
	share, err := ShareURL(publishing.ShareLinkedIn, "https://reader.example/v/report?page=2", "Annual report")
	if err != nil || !strings.HasPrefix(share, "https://www.linkedin.com/") || !strings.Contains(share, "url=https%3A%2F%2F") {
		t.Fatalf("unexpected share URL %q, %v", share, err)
	}
	if got := VideoEmbedURL("https://www.youtube.com/watch?v=dQw4w9WgXcQ"); got != "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" {
		t.Fatalf("unexpected YouTube embed %q", got)
	}
	if got := VideoEmbedURL("javascript:alert(1)"); got != "" {
		t.Fatalf("unsafe video URL accepted: %q", got)
	}
}

func TestQRCodeSVGAndPNG(t *testing.T) {
	const content = "https://reader.example/v/annual-report?page=12"
	matrix, err := makeQRMatrix(content)
	if err != nil {
		t.Fatal(err)
	}
	if matrix.size != matrix.version*4+17 || len(matrix.modules) != matrix.size {
		t.Fatalf("invalid matrix dimensions: %+v", matrix)
	}
	for _, point := range [][2]int{{0, 0}, {matrix.size - 7, 0}, {0, matrix.size - 7}} {
		if !matrix.function[point[1]][point[0]] || !matrix.modules[point[1]][point[0]] {
			t.Fatalf("missing finder pattern at %v", point)
		}
	}
	svg, err := QRCodeSVG(content, 4, 4)
	if err != nil || !bytes.Contains(svg, []byte("<svg")) || !bytes.Contains(svg, []byte("<path")) {
		t.Fatalf("invalid SVG QR: %v", err)
	}
	encoded, err := QRCodePNG(content, 256)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != image.Bounds().Dy() || image.Bounds().Dx() > 256 {
		t.Fatalf("unexpected PNG dimensions: %v", image.Bounds())
	}
}

func TestSitemapAndRSSFilterInvalidLinks(t *testing.T) {
	sitemap, err := Sitemap([]SitemapEntry{{Location: "https://reader.example/v/report"}, {Location: "javascript:alert(1)"}})
	if err != nil || !bytes.Contains(sitemap, []byte("<loc>https://reader.example/v/report</loc>")) || bytes.Contains(sitemap, []byte("javascript")) {
		t.Fatalf("unexpected sitemap: %s %v", sitemap, err)
	}
	feed, err := RSS("Reports", "https://reader.example/catalog", "Latest", []FeedItem{{Title: "Report", Link: "https://reader.example/v/report"}})
	if err != nil || !bytes.Contains(feed, []byte("<title>Report</title>")) {
		t.Fatalf("unexpected RSS: %s %v", feed, err)
	}
}
