package converter

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTesseractOCRFallback(t *testing.T) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("Tesseract is not installed")
	}
	imagePath := filepath.Join(t.TempDir(), "textless-page.png")
	writeOCRTestImage(t, imagePath)
	converter := NewWithOptions("", t.TempDir(), DefaultOptions())
	texts, used, warnings, err := converter.applyOCR(context.Background(), []string{""}, []string{imagePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || !used[1] || !strings.Contains(strings.ToUpper(texts[0]), "HELLO") {
		t.Errorf("unexpected OCR result %#v, used %#v, warnings %#v", texts, used, warnings)
	}
}

func writeOCRTestImage(t *testing.T, path string) {
	t.Helper()
	const scale = 12
	glyphs := map[rune][]string{
		'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
		'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
		'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
		'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	}
	img := image.NewRGBA(image.Rect(0, 0, 500, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 500; x++ {
			img.Set(x, y, color.White)
		}
	}
	xOffset := 40
	for _, char := range "HELLO" {
		glyph := glyphs[char]
		for y, row := range glyph {
			for x, pixel := range row {
				if pixel != '1' {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						img.Set(xOffset+x*scale+dx, 40+y*scale+dy, color.Black)
					}
				}
			}
		}
		xOffset += 6 * scale
	}
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
