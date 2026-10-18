package converter

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (c *Converter) documentToPDF(ctx context.Context, srcPath, stagingDir string) (string, []Warning, error) {
	if c.libreOfficeBin == "" {
		return "", nil, errors.New("libreoffice executable is not configured")
	}
	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		return "", nil, err
	}
	profileRoot := c.tmpDir
	if profileRoot == "" {
		profileRoot = os.TempDir()
	}
	if err := os.MkdirAll(profileRoot, 0755); err != nil {
		return "", nil, err
	}
	profileDir, err := os.MkdirTemp(profileRoot, "lo-profile-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(profileDir)
	outputDir, err := os.MkdirTemp(stagingDir, ".lo-output-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(outputDir)
	absProfile, err := filepath.Abs(profileDir)
	if err != nil {
		return "", nil, err
	}
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return "", nil, err
	}

	result, err := c.runCommand(ctx, c.libreOfficeBin,
		"--headless",
		"--invisible",
		"--nologo",
		"--nodefault",
		"--nofirststartwizard",
		"--norestore",
		fmt.Sprintf("-env:UserInstallation=%s", fileURL(absProfile)),
		"--convert-to", "pdf",
		"--outdir", absOut,
		absSrc,
	)
	if err != nil {
		return "", nil, fmt.Errorf("libreoffice %s to PDF: %w", strings.ToLower(filepath.Ext(srcPath)), commandError(c.libreOfficeBin, err, result))
	}
	warnings := append(warningsFromCommand("libreoffice", result.stdout, 0), warningsFromCommand("libreoffice", result.stderr, 0)...)

	baseName := strings.TrimSuffix(filepath.Base(absSrc), filepath.Ext(absSrc))
	pdfPath := filepath.Join(absOut, baseName+".pdf")
	if info, statErr := os.Stat(pdfPath); statErr != nil || info.Size() == 0 {
		if statErr == nil {
			statErr = errors.New("empty PDF")
		}
		return "", warnings, fmt.Errorf("LibreOffice produced no usable PDF: %w; output: %s", statErr, strings.TrimSpace(string(result.stdout)))
	}
	convertedPath := filepath.Join(stagingDir, "converted.pdf")
	if _, err := copyFileAtomic(pdfPath, convertedPath); err != nil {
		return "", warnings, err
	}
	if err := validatePDFFile(stagingDir, "converted.pdf"); err != nil {
		return "", warnings, err
	}
	return convertedPath, warnings, nil
}

func (c *Converter) pptxToPDF(ctx context.Context, srcPath, outDir string) (string, error) {
	path, _, err := c.documentToPDF(ctx, srcPath, outDir)
	return path, err
}

func fileURL(path string) string {
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

func isToolUnavailable(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}
