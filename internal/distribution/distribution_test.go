package distribution

import (
	"bytes"
	"image/png"
	"net/url"
	"strings"
	"testing"

	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
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
