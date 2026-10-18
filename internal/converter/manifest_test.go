package converter

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateArtifactsAndDetectTampering(t *testing.T) {
	dir := t.TempDir()
	manifest := createTestArtifacts(t, dir, [][2]int{{40, 60}, {80, 30}})
	if err := ValidateArtifacts(dir, manifest); err != nil {
		t.Fatalf("valid artifacts rejected: %v", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "pages", "page-1.png"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("tampered"))
	_ = file.Close()
	if err := ValidateArtifacts(dir, manifest); !errors.Is(err, ErrArtifactValidation) {
		t.Fatalf("tampering was not detected: %v", err)
	}
}

func TestNormalizePageTextsPreservesBlankPages(t *testing.T) {
	texts, err := normalizePageTexts("one\f\fthree\f", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "", "three", ""}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("page %d: got %q, want %q", i+1, texts[i], want[i])
		}
	}
}

func TestParsePositionedText(t *testing.T) {
	xml := `<?xml version="1.0"?><html><body><doc><page width="612" height="792"><flow><block><line xMin="1" yMin="2" xMax="3" yMax="4"><word xMin="10.5" yMin="20" xMax="30" yMax="32.5">Hello</word></line></block></flow></page></doc></body></html>`
	pages, err := parsePositionedText([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages[1]) != 1 || pages[1][0].Text != "Hello" || pages[1][0].Rect.Width != 19.5 || pages[1][0].Rect.Height != 12.5 {
		t.Errorf("unexpected positioned text: %+v", pages)
	}
}

func TestParseLinksAndOutlineXML(t *testing.T) {
	xml := `<pdf2xml><outline><item title="Chapter" page="2"><item title="Nested" dest="named"/></item></outline><page number="1"><a href="https://example.com"><text top="10" left="20" width="30" height="12">Link</text></a><a href="document-2.html"><text top="30" left="20" width="40" height="12">Next</text></a><a href="javascript:alert(1)"><text top="50" left="20" width="40" height="12">Unsafe</text></a></page></pdf2xml>`
	root, err := parseXMLTree([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	page := descendants(root, "page")[0]
	var links []Link
	for _, anchor := range descendants(page, "a") {
		if link, ok := classifyLink(anchor.attr("href"), map[string]int{"named": 2}); ok {
			rect, _ := anchorRect(anchor)
			link.Rect = rect
			links = append(links, link)
		}
	}
	if len(links) != 2 || links[0].URI != "https://example.com" || links[1].TargetPage != 2 {
		t.Errorf("unexpected links: %+v", links)
	}
	outline := parseOutline(root, map[string]int{"named": 2})
	if len(outline) != 1 || len(outline[0].Children) != 1 || outline[0].Children[0].TargetPage != 2 {
		t.Errorf("unexpected outline: %+v", outline)
	}
}

func createTestArtifacts(t *testing.T, dir string, sizes [][2]int) *Manifest {
	t.Helper()
	for _, name := range []string{"pages", "thumbs", "renditions"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "converted.pdf"), []byte("%PDF-1.4\n%%EOF\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "original.pdf"), []byte("%PDF-1.4\n%%EOF\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{
		SchemaVersion: ManifestSchemaVersion,
		CreatedAt:     time.Now().UTC(),
		SourceFormat:  "pdf",
		SourcePDF:     "converted.pdf",
		Original:      "original.pdf",
		ConversionDPI: 72,
		ThumbnailDPI:  36,
		PageCount:     len(sizes),
		Pages:         make([]PageManifest, len(sizes)),
	}
	texts := make([]string, len(sizes))
	for i, size := range sizes {
		pageNumber := i + 1
		pagePNG := filepath.Join(dir, "pages", "page-"+string(rune('0'+pageNumber))+".png")
		thumbPNG := filepath.Join(dir, "thumbs", "page-"+string(rune('0'+pageNumber))+".png")
		writeTestPNG(t, pagePNG, size[0], size[1])
		thumbWidth, thumbHeight := max(1, size[0]/2), max(1, size[1]/2)
		writeTestPNG(t, thumbPNG, thumbWidth, thumbHeight)
		pageJPG := filepath.Join(dir, "renditions", "page-"+string(rune('0'+pageNumber))+".jpg")
		thumbJPG := filepath.Join(dir, "renditions", "thumb-"+string(rune('0'+pageNumber))+".jpg")
		if err := writeJPEG(pagePNG, pageJPG, 85); err != nil {
			t.Fatal(err)
		}
		if err := writeJPEG(thumbPNG, thumbJPG, 85); err != nil {
			t.Fatal(err)
		}
		pageName := "page-" + string(rune('0'+pageNumber)) + ".png"
		pageJPEGName := "page-" + string(rune('0'+pageNumber)) + ".jpg"
		thumbJPEGName := "thumb-" + string(rune('0'+pageNumber)) + ".jpg"
		pagePNGRendition, err := makeRendition(dir, "page", "png", filepath.Join("pages", pageName), 0)
		if err != nil {
			t.Fatal(err)
		}
		pageJPEGRendition, err := makeRendition(dir, "page", "jpeg", filepath.Join("renditions", pageJPEGName), 85)
		if err != nil {
			t.Fatal(err)
		}
		thumbPNGRendition, err := makeRendition(dir, "thumbnail", "png", filepath.Join("thumbs", pageName), 0)
		if err != nil {
			t.Fatal(err)
		}
		thumbJPEGRendition, err := makeRendition(dir, "thumbnail", "jpeg", filepath.Join("renditions", thumbJPEGName), 85)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Pages[i] = PageManifest{
			Number:       pageNumber,
			Width:        size[0],
			Height:       size[1],
			WidthPoints:  float64(size[0]),
			HeightPoints: float64(size[1]),
			TextSource:   "none",
			Images:       []Rendition{pagePNGRendition, pageJPEGRendition, thumbPNGRendition, thumbJPEGRendition},
		}
	}
	manifest.Width = manifest.Pages[0].Width
	manifest.Height = manifest.Pages[0].Height
	if err := writePageTexts(dir, texts); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func writeTestPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
