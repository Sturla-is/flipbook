package converter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConvertStagedPDFWithPoppler(t *testing.T) {
	requirePoppler(t)
	root := t.TempDir()
	source := filepath.Join(root, "mixed.pdf")
	if err := os.WriteFile(source, generatedTestPDF(), 0644); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.ConversionDPI = 72
	options.ThumbnailDPI = 36
	options.Timeout = 2 * time.Minute
	options.OCR.Disabled = true
	converter := NewWithOptions("", filepath.Join(root, "tmp"), options)
	staging := filepath.Join(root, "staging")
	result, err := converter.ConvertStaged(context.Background(), source, staging)
	if err != nil {
		t.Fatalf("ConvertStaged failed: %v", err)
	}
	if result.PageCount != 2 || len(result.Pages) != 2 {
		t.Fatalf("got %d pages and %d page manifests", result.PageCount, len(result.Pages))
	}
	if result.Width != 612 || result.Height != 792 {
		t.Errorf("unexpected first-page dimensions %dx%d", result.Width, result.Height)
	}
	if got := result.Pages[1]; got.Width != 792 || got.Height != 612 {
		t.Errorf("mixed page geometry not retained: %+v", got)
	}
	for _, page := range result.Pages {
		if len(page.Images) != 4 {
			t.Errorf("page %d has %d renditions", page.Number, len(page.Images))
		}
		if len(page.Text) == 0 {
			t.Errorf("page %d has no positioned text", page.Number)
		}
	}
	texts, err := readPageTexts(staging)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(texts[0], "Hello first page") || !strings.Contains(texts[1], "Second landscape page") {
		t.Errorf("unexpected page texts: %#v", texts)
	}
	if len(result.Pages[0].Links) == 0 {
		output, _ := exec.Command(options.Tools.PDFToHTML, "-xml", "-stdout", "-i", "-hidden", "-noframes", "-noroundcoord", "-zoom", "1.0", source).CombinedOutput()
		t.Logf("pdftohtml output: %s", output)
	}
	if !hasTestLink(result.Pages[0].Links, "external", "https://example.com", 0) {
		t.Errorf("external link missing: %+v", result.Pages[0].Links)
	}
	if !hasTestLink(result.Pages[0].Links, "internal", "", 2) {
		t.Errorf("internal link missing: %+v", result.Pages[0].Links)
	}
	manifest, err := LoadManifest(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Outline) == 0 || manifest.Outline[0].TargetPage != 2 {
		t.Errorf("PDF outline missing: %+v", manifest.Outline)
	}
	if err := ValidateArtifacts(staging, manifest); err != nil {
		t.Fatalf("generated artifacts did not validate: %v", err)
	}
}

func TestLegacyConvertPromotesCompatibleLayout(t *testing.T) {
	requirePoppler(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.pdf")
	if err := os.WriteFile(source, generatedTestPDF(), 0644); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.ConversionDPI = 72
	options.ThumbnailDPI = 36
	options.OCR.Disabled = true
	converter := NewWithOptions("", filepath.Join(root, "tmp"), options)
	output := filepath.Join(root, "book")
	result, err := converter.Convert(context.Background(), source, filepath.Join(output, "pages"), filepath.Join(output, "thumbs"))
	if err != nil {
		t.Fatal(err)
	}
	if result.StagingDir != "" {
		t.Errorf("legacy result retained staging path %q", result.StagingDir)
	}
	for _, name := range []string{"pages/page-1.png", "pages/page-2.png", "thumbs/page-1.png", "renditions/page-1.jpg", "renditions/thumb-1.jpg", "text.json", "manifest.json", "converted.pdf", "original.pdf"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Errorf("missing compatible artifact %s: %v", name, err)
		}
	}
	stale, err := filepath.Glob(filepath.Join(output, ".conversion-*"))
	if err != nil || len(stale) != 0 {
		t.Errorf("staging was not cleaned: %v, %v", stale, err)
	}
	before, err := fileSHA256(filepath.Join(output, "pages", "page-1.png"))
	if err != nil {
		t.Fatal(err)
	}
	badSource := filepath.Join(root, "bad.pdf")
	if err := os.WriteFile(badSource, []byte("%PDF-corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := converter.Convert(context.Background(), badSource, filepath.Join(output, "pages"), filepath.Join(output, "thumbs")); err == nil {
		t.Fatal("corrupt replacement unexpectedly succeeded")
	}
	after, err := fileSHA256(filepath.Join(output, "pages", "page-1.png"))
	if err != nil || after != before {
		t.Errorf("failed replacement changed live page: before %s, after %s, %v", before, after, err)
	}
}

func TestUnsupportedFormatFailsBeforeExternalTools(t *testing.T) {
	converter := NewWithOptions("", t.TempDir(), DefaultOptions())
	_, err := converter.ConvertStaged(context.Background(), "workbook.xlsx", filepath.Join(t.TempDir(), "staging"))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("got %v, want ErrUnsupportedFormat", err)
	}
}

func TestDiskQuotaStopsRendering(t *testing.T) {
	requirePoppler(t)
	root := t.TempDir()
	source := filepath.Join(root, "source.pdf")
	data := generatedTestPDF()
	if err := os.WriteFile(source, data, 0644); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.ConversionDPI = 72
	options.ThumbnailDPI = 36
	options.OCR.Disabled = true
	options.MaxOutputBytes = int64(len(data)*2 + 1)
	converter := NewWithOptions("", filepath.Join(root, "tmp"), options)
	_, err := converter.ConvertStaged(context.Background(), source, filepath.Join(root, "staging"))
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("disk quota returned %v", err)
	}
}

func TestLibreOfficeRTFConversion(t *testing.T) {
	requirePoppler(t)
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("LibreOffice is not installed")
	}
	root := t.TempDir()
	source := filepath.Join(root, "document.rtf")
	rtf := `{\rtf1\ansi\deff0{\fonttbl{\f0 Helvetica;}}\f0\fs24 Hello RTF conversion\par}`
	if err := os.WriteFile(source, []byte(rtf), 0644); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.ConversionDPI = 72
	options.ThumbnailDPI = 36
	options.OCR.Disabled = true
	converter := NewWithOptions("soffice", filepath.Join(root, "tmp"), options)
	staging := filepath.Join(root, "staging")
	result, err := converter.ConvertStaged(context.Background(), source, staging)
	if err != nil {
		t.Fatalf("RTF conversion failed: %v", err)
	}
	if result.PageCount != 1 {
		t.Errorf("RTF produced %d pages", result.PageCount)
	}
	texts, err := readPageTexts(staging)
	if err != nil || len(texts) != 1 || !strings.Contains(texts[0], "Hello RTF conversion") {
		t.Errorf("unexpected RTF text %#v, %v", texts, err)
	}
}

func TestAdditionalDocumentFormatsAreExplicit(t *testing.T) {
	extensions := SupportedExtensions()
	for _, required := range []string{".doc", ".docx", ".odt", ".odp", ".rtf", ".ppt", ".pptx", ".pdf"} {
		found := false
		for _, extension := range extensions {
			if extension == required {
				found = true
			}
		}
		if !found {
			t.Errorf("required extension %s is not supported", required)
		}
	}
}

func TestOCRFallbackHook(t *testing.T) {
	options := DefaultOptions()
	options.OCR.Run = func(_ context.Context, imagePath, language string) (string, error) {
		if language != "eng" || imagePath == "" {
			t.Fatalf("unexpected OCR arguments %q %q", imagePath, language)
		}
		return "Recovered text", nil
	}
	converter := NewWithOptions("", t.TempDir(), options)
	texts, used, warnings, err := converter.applyOCR(context.Background(), []string{"", "embedded"}, []string{"one.png", "two.png"})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("OCR failed: %v, %#v", err, warnings)
	}
	if texts[0] != "Recovered text" || texts[1] != "embedded" || !used[1] || used[2] {
		t.Errorf("unexpected OCR result %#v, %#v", texts, used)
	}
}

func requirePoppler(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pdfinfo", "pdftoppm", "pdftotext", "pdftohtml"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

func hasTestLink(links []Link, kind, uri string, page int) bool {
	for _, link := range links {
		if link.Kind != kind {
			continue
		}
		if kind == "external" && link.URI == uri && link.Rect.Width > 0 {
			return true
		}
		if kind == "internal" && link.TargetPage == page && link.Rect.Width > 0 {
			return true
		}
	}
	return false
}

func generatedTestPDF() []byte {
	pageOne := "BT /F1 18 Tf 72 720 Td (Hello first page) Tj ET\n" +
		"BT /F1 12 Tf 72 690 Td (External link) Tj ET\n" +
		"BT /F1 12 Tf 72 650 Td (Go to page two) Tj ET"
	pageTwo := "BT /F1 18 Tf 72 540 Td (Second landscape page) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /Outlines 6 0 R /PageMode /UseOutlines >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 7 0 R /Annots [8 0 R 9 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 792 612] /Resources << /Font << /F1 5 0 R >> >> /Contents 10 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Outlines /First 11 0 R /Last 11 0 R /Count 1 >>",
		pdfTestStream(pageOne),
		"<< /Type /Annot /Subtype /Link /Rect [65 680 190 705] /Border [0 0 0] /A << /S /URI /URI (https://example.com) >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [65 640 200 665] /Border [0 0 0] /Dest [4 0 R /XYZ 0 612 null] >>",
		pdfTestStream(pageTwo),
		"<< /Title (Chapter Two) /Parent 6 0 R /Dest [4 0 R /XYZ 0 612 null] >>",
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects)+1)
	for i, object := range objects {
		offsets[i+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n", len(objects)+1)
	output.WriteString("0000000000 65535 f \n")
	for i := 1; i < len(offsets); i++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}

func pdfTestStream(content string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
}
