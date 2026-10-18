package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// DiscoverTesseract resolves an explicitly configured binary or discovers a
// system installation. It does not run the executable.
func DiscoverTesseract(configured string) (string, error) {
	if configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("configured tesseract %q: %w", configured, err)
		}
		return path, nil
	}
	if path, err := exec.LookPath("tesseract"); err == nil {
		return path, nil
	}
	candidates := []string{
		"/usr/bin/tesseract",
		"/usr/local/bin/tesseract",
		`C:\Program Files\Tesseract-OCR\tesseract.exe`,
		`C:\Program Files (x86)\Tesseract-OCR\tesseract.exe`,
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func (c *Converter) applyOCR(ctx context.Context, texts []string, imagePaths []string) ([]string, map[int]bool, []Warning, error) {
	config := c.options.OCR
	ocrUsed := make(map[int]bool)
	if config.Disabled {
		return texts, ocrUsed, nil, nil
	}
	needsOCR := make([]int, 0)
	for i, text := range texts {
		if utf8.RuneCountInString(strings.TrimSpace(text)) < config.MinTextRunes {
			needsOCR = append(needsOCR, i)
		}
	}
	if len(needsOCR) == 0 {
		return texts, ocrUsed, nil, nil
	}

	runner := config.Run
	var warnings []Warning
	if runner == nil {
		binary, err := DiscoverTesseract(config.Binary)
		if err != nil {
			if config.Required {
				return texts, ocrUsed, nil, newConversionError(err, "OCR discovery", fmt.Errorf("%d pages require OCR", len(needsOCR)))
			}
			warnings = append(warnings, Warning{Code: "ocr_unavailable", Message: fmt.Sprintf("tesseract not found; %d pages have no embedded text", len(needsOCR))})
			return texts, ocrUsed, warnings, nil
		}
		runner = c.tesseractOCR(binary)
	}

	for _, index := range needsOCR {
		page := index + 1
		pageCtx := ctx
		cancel := func() {}
		if config.Timeout > 0 {
			pageCtx, cancel = context.WithTimeout(ctx, config.Timeout)
		}
		text, err := runner(pageCtx, imagePaths[index], config.Language)
		cancel()
		if err != nil {
			if config.Required {
				return texts, ocrUsed, warnings, pageConversionError(err, "OCR", page, nil)
			}
			warnings = append(warnings, Warning{Code: "ocr_failed", Message: err.Error(), Page: page})
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		texts[index] = text
		ocrUsed[page] = true
	}
	return texts, ocrUsed, warnings, nil
}

func (c *Converter) tesseractOCR(binary string) OCRFunc {
	return func(ctx context.Context, imagePath, language string) (string, error) {
		if language == "" {
			language = "eng"
		}
		result, err := c.runCommand(ctx, binary, filepath.Clean(imagePath), "stdout", "-l", language, "--psm", "3")
		if err != nil {
			return "", commandError("tesseract", err, result)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ctx.Err()
		}
		return string(result.stdout), nil
	}
}
