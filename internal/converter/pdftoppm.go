package converter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type pdfPageGeometry struct {
	widthPoints  float64
	heightPoints float64
	rotation     int
}

type pdfDocumentInfo struct {
	pageCount int
	encrypted bool
	pages     []pdfPageGeometry
}

func (c *Converter) pdfToPNG(ctx context.Context, pdfPath, outDir string, dpi int) (int, error) {
	info, _, err := c.inspectPDF(ctx, pdfPath)
	if err != nil {
		return 0, err
	}
	_, err = c.renderPDFPages(ctx, pdfPath, outDir, dpi, info.pageCount, nil)
	return info.pageCount, err
}

// renderPDFPages deliberately renders one page at a time. Besides keeping
// memory use bounded, this preserves the page-N.png names expected by legacy
// viewers even for documents with many pages.
func (c *Converter) renderPDFPages(ctx context.Context, pdfPath, outDir string, dpi, pageCount int, budget *outputBudget) ([]Warning, error) {
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	absPDF, err := filepath.Abs(pdfPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absOut, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	var warnings []Warning
	for page := 1; page <= pageCount; page++ {
		outputPrefix := filepath.Join(absOut, fmt.Sprintf("page-%d", page))
		result, err := c.runCommand(ctx, c.options.Tools.PDFToPPM,
			"-singlefile",
			"-f", strconv.Itoa(page),
			"-l", strconv.Itoa(page),
			"-png",
			"-rx", strconv.Itoa(dpi),
			"-ry", strconv.Itoa(dpi),
			absPDF,
			outputPrefix,
		)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) || ctx.Err() != nil {
				return warnings, pageConversionError(nil, "pdftoppm", page, commandError(c.options.Tools.PDFToPPM, err, result))
			}
			return warnings, pageConversionError(ErrCorruptPDF, "pdftoppm", page, commandError(c.options.Tools.PDFToPPM, err, result))
		}
		warnings = append(warnings, warningsFromCommand("pdftoppm", result.stderr, page)...)
		outputPath := outputPrefix + ".png"
		if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
			if err == nil {
				err = errors.New("empty output file")
			}
			return warnings, pageConversionError(ErrArtifactValidation, "pdftoppm produced no output", page, err)
		}
		if err := budget.add(outputPath); err != nil {
			return warnings, pageConversionError(nil, "disk limit", page, err)
		}
	}
	return warnings, nil
}

func (c *Converter) inspectPDF(ctx context.Context, pdfPath string) (pdfDocumentInfo, []Warning, error) {
	var info pdfDocumentInfo
	result, err := c.runCommand(ctx, c.options.Tools.PDFInfo, pdfPath)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || ctx.Err() != nil {
			return info, nil, commandError(c.options.Tools.PDFInfo, err, result)
		}
		return info, nil, classifyPDFToolError("pdfinfo", err, result.stderr)
	}
	warnings := warningsFromCommand("pdfinfo", result.stderr, 0)
	if err := parsePDFInfo(result.stdout, &info); err != nil {
		return info, warnings, newConversionError(ErrCorruptPDF, "pdfinfo", err)
	}
	if info.pageCount < 1 {
		return info, warnings, newConversionError(ErrCorruptPDF, "pdfinfo", errors.New("PDF has no pages"))
	}

	detailed, err := c.runCommand(ctx, c.options.Tools.PDFInfo,
		"-f", "1", "-l", strconv.Itoa(info.pageCount), pdfPath)
	if err == nil {
		warnings = append(warnings, warningsFromCommand("pdfinfo", detailed.stderr, 0)...)
		if err := parsePDFInfo(detailed.stdout, &info); err != nil {
			return info, warnings, newConversionError(ErrCorruptPDF, "pdfinfo page geometry", err)
		}
	} else {
		warnings = append(warnings, Warning{Code: "page_geometry_unavailable", Message: commandError(c.options.Tools.PDFInfo, err, detailed).Error()})
	}
	if len(info.pages) == 0 {
		return info, warnings, newConversionError(ErrCorruptPDF, "pdfinfo", errors.New("PDF page geometry is unavailable"))
	}
	for len(info.pages) < info.pageCount {
		info.pages = append(info.pages, info.pages[len(info.pages)-1])
	}
	return info, warnings, nil
}

func parsePDFInfo(output []byte, info *pdfDocumentInfo) error {
	var defaultGeometry *pdfPageGeometry
	if len(info.pages) == 0 && info.pageCount > 0 {
		info.pages = make([]pdfPageGeometry, info.pageCount)
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "Pages:":
			pages, err := strconv.Atoi(fields[1])
			if err != nil || pages < 1 {
				return errors.New("pdfinfo did not report a valid page count")
			}
			info.pageCount = pages
			if len(info.pages) == 0 {
				info.pages = make([]pdfPageGeometry, pages)
			}
		case "Encrypted:":
			info.encrypted = strings.EqualFold(fields[1], "yes")
		case "Page":
			if len(fields) >= 7 && fields[1] == "size:" {
				width, errWidth := strconv.ParseFloat(fields[2], 64)
				height, errHeight := strconv.ParseFloat(fields[4], 64)
				if errWidth == nil && errHeight == nil && width > 0 && height > 0 {
					defaultGeometry = &pdfPageGeometry{widthPoints: width, heightPoints: height}
				}
				continue
			}
			if len(fields) < 3 {
				continue
			}
			page, err := strconv.Atoi(fields[1])
			if err != nil || page < 1 {
				continue
			}
			for len(info.pages) < page {
				info.pages = append(info.pages, pdfPageGeometry{})
			}
			switch fields[2] {
			case "size:":
				if len(fields) < 7 {
					continue
				}
				width, errWidth := strconv.ParseFloat(fields[2+1], 64)
				height, errHeight := strconv.ParseFloat(fields[2+3], 64)
				if errWidth != nil || errHeight != nil || width <= 0 || height <= 0 {
					return fmt.Errorf("invalid geometry for page %d", page)
				}
				info.pages[page-1].widthPoints = width
				info.pages[page-1].heightPoints = height
			case "rot:":
				rotation, err := strconv.Atoi(fields[3])
				if err == nil {
					info.pages[page-1].rotation = ((rotation % 360) + 360) % 360
				}
			}
		}
	}
	if info.pageCount > 0 && len(info.pages) == 0 {
		info.pages = make([]pdfPageGeometry, info.pageCount)
	}
	if defaultGeometry != nil {
		for i := range info.pages {
			if info.pages[i].widthPoints == 0 {
				info.pages[i].widthPoints = defaultGeometry.widthPoints
				info.pages[i].heightPoints = defaultGeometry.heightPoints
			}
		}
	}
	for i := range info.pages {
		if info.pages[i].widthPoints <= 0 || info.pages[i].heightPoints <= 0 {
			return fmt.Errorf("missing geometry for page %d", i+1)
		}
	}
	return nil
}

func (g pdfPageGeometry) displayPoints() (float64, float64) {
	if g.rotation%180 == 90 {
		return g.heightPoints, g.widthPoints
	}
	return g.widthPoints, g.heightPoints
}

func (g pdfPageGeometry) estimatedPixels(dpi int) int64 {
	width, height := g.displayPoints()
	pixels := math.Ceil(width*float64(dpi)/72) * math.Ceil(height*float64(dpi)/72)
	if pixels >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(pixels)
}

func pdfPageCount(ctx context.Context, pdfPath string) (int, error) {
	c := NewWithOptions("", "", DefaultOptions())
	info, _, err := c.inspectPDF(ctx, pdfPath)
	if err != nil {
		return 0, err
	}
	return info.pageCount, nil
}
