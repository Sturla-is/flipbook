package converter

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func (c *Converter) extractTextArtifacts(ctx context.Context, pdfPath string, pageCount int) ([]string, map[int][]TextSpan, []Warning) {
	texts := make([]string, pageCount)
	var warnings []Warning
	result, err := c.runCommand(ctx, c.options.Tools.PDFToText, "-layout", pdfPath, "-")
	if err != nil {
		warnings = append(warnings, Warning{Code: "text_extraction_failed", Message: commandError(c.options.Tools.PDFToText, err, result).Error()})
		return texts, nil, warnings
	}
	warnings = append(warnings, warningsFromCommand("pdftotext", result.stderr, 0)...)
	texts, err = normalizePageTexts(string(result.stdout), pageCount)
	if err != nil {
		warnings = append(warnings, Warning{Code: "text_extraction_failed", Message: err.Error()})
		texts = make([]string, pageCount)
		return texts, nil, warnings
	}

	spans := make(map[int][]TextSpan)
	result, err = c.runCommand(ctx, c.options.Tools.PDFToText, "-bbox-layout", pdfPath, "-")
	if err != nil {
		warnings = append(warnings, Warning{Code: "positioned_text_unavailable", Message: commandError(c.options.Tools.PDFToText, err, result).Error()})
		return texts, spans, warnings
	}
	warnings = append(warnings, warningsFromCommand("pdftotext", result.stderr, 0)...)
	spans, err = parsePositionedText(result.stdout)
	if err != nil {
		warnings = append(warnings, Warning{Code: "positioned_text_unavailable", Message: err.Error()})
		spans = make(map[int][]TextSpan)
	}
	return texts, spans, warnings
}

func normalizePageTexts(output string, pageCount int) ([]string, error) {
	if pageCount < 1 {
		return nil, fmt.Errorf("invalid page count %d", pageCount)
	}
	rawPages := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\f")
	for len(rawPages) > 0 && strings.TrimSpace(rawPages[len(rawPages)-1]) == "" {
		rawPages = rawPages[:len(rawPages)-1]
	}
	if len(rawPages) > pageCount {
		return nil, fmt.Errorf("pdftotext returned %d non-empty pages for a %d-page PDF", len(rawPages), pageCount)
	}
	texts := make([]string, pageCount)
	for i := range rawPages {
		texts[i] = strings.TrimSpace(rawPages[i])
	}
	return texts, nil
}

func parsePositionedText(data []byte) (map[int][]TextSpan, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	pages := make(map[int][]TextSpan)
	pageNumber := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse pdftotext bbox output: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "page":
			pageNumber++
			if pageNumber == 0 {
				return nil, fmt.Errorf("invalid page in bbox output")
			}
		case "word":
			if pageNumber < 1 {
				continue
			}
			var text string
			if err := decoder.DecodeElement(&text, &start); err != nil {
				return nil, fmt.Errorf("decode bbox word: %w", err)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			rect, err := rectFromAttributes(start.Attr, "xMin", "yMin", "xMax", "yMax")
			if err != nil {
				return nil, fmt.Errorf("decode bbox word on page %d: %w", pageNumber, err)
			}
			pages[pageNumber] = append(pages[pageNumber], TextSpan{Text: text, Rect: rect})
		}
	}
	return pages, nil
}

func rectFromAttributes(attrs []xml.Attr, xName, yName, maxXName, maxYName string) (Rect, error) {
	values := make(map[string]float64, 4)
	for _, name := range []string{xName, yName, maxXName, maxYName} {
		found := false
		for _, attr := range attrs {
			if attr.Name.Local != name {
				continue
			}
			value, err := strconv.ParseFloat(attr.Value, 64)
			if err != nil {
				return Rect{}, fmt.Errorf("invalid %s coordinate", name)
			}
			values[name] = value
			found = true
			break
		}
		if !found {
			return Rect{}, fmt.Errorf("missing %s coordinate", name)
		}
	}
	return Rect{
		X:      values[xName],
		Y:      values[yName],
		Width:  values[maxXName] - values[xName],
		Height: values[maxYName] - values[yName],
	}, nil
}
