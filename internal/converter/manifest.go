package converter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ManifestSchemaVersion = 1

// Rect is a rectangle in PDF points with its origin at the top-left of a page.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// TextSpan is positioned text suitable for accessible reading and overlays.
type TextSpan struct {
	Text string `json:"text"`
	Rect Rect   `json:"rect"`
}

// Link describes an external URI or an internal page destination.
type Link struct {
	Kind        string `json:"kind"`
	URI         string `json:"uri,omitempty"`
	TargetPage  int    `json:"target_page,omitempty"`
	Destination string `json:"destination,omitempty"`
	Rect        Rect   `json:"rect"`
}

// OutlineItem is a navigable PDF outline entry.
type OutlineItem struct {
	Title       string        `json:"title"`
	TargetPage  int           `json:"target_page,omitempty"`
	Destination string        `json:"destination,omitempty"`
	Children    []OutlineItem `json:"children,omitempty"`
}

// Rendition identifies one immutable image artifact.
type Rendition struct {
	Role    string `json:"role"`
	Format  string `json:"format"`
	Path    string `json:"path"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
	Quality int    `json:"quality,omitempty"`
}

// PageManifest preserves per-page geometry, text, links, and image variants.
type PageManifest struct {
	Number       int         `json:"number"`
	Width        int         `json:"width"`
	Height       int         `json:"height"`
	WidthPoints  float64     `json:"width_points"`
	HeightPoints float64     `json:"height_points"`
	Rotation     int         `json:"rotation"`
	TextSource   string      `json:"text_source"`
	Text         []TextSpan  `json:"text,omitempty"`
	Links        []Link      `json:"links,omitempty"`
	Images       []Rendition `json:"images"`
}

// Warning is a non-fatal conversion diagnostic suitable for display in admin.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Page    int    `json:"page,omitempty"`
}

// Manifest is written to manifest.json in every staged conversion.
type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	CreatedAt     time.Time      `json:"created_at"`
	SourceFormat  string         `json:"source_format"`
	SourcePDF     string         `json:"source_pdf"`
	Original      string         `json:"original,omitempty"`
	ConversionDPI int            `json:"conversion_dpi"`
	ThumbnailDPI  int            `json:"thumbnail_dpi"`
	PageCount     int            `json:"page_count"`
	Width         int            `json:"width"`
	Height        int            `json:"height"`
	TextAvailable bool           `json:"text_available"`
	Pages         []PageManifest `json:"pages"`
	Outline       []OutlineItem  `json:"outline,omitempty"`
	Warnings      []Warning      `json:"warnings,omitempty"`
}

// LoadManifest reads and decodes a conversion manifest from a staging or
// revision directory.
func LoadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &manifest, nil
}

// ValidateArtifacts verifies every required page, thumbnail, rendition, text,
// PDF, and manifest artifact, including dimensions, sizes, and SHA-256 sums.
func ValidateArtifacts(dir string, manifest *Manifest) error {
	if manifest == nil {
		return newConversionError(ErrArtifactValidation, "validate", errors.New("nil manifest"))
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return newConversionError(ErrArtifactValidation, "validate", fmt.Errorf("unsupported schema version %d", manifest.SchemaVersion))
	}
	if manifest.PageCount < 1 || len(manifest.Pages) != manifest.PageCount {
		return newConversionError(ErrArtifactValidation, "validate", errors.New("page count does not match page manifest"))
	}
	if manifest.SourcePDF == "" {
		return newConversionError(ErrArtifactValidation, "validate", errors.New("missing converted PDF"))
	}
	if err := validatePDFFile(dir, manifest.SourcePDF); err != nil {
		return err
	}
	if manifest.Original != "" {
		path, err := artifactPath(dir, manifest.Original)
		if err != nil {
			return newConversionError(ErrArtifactValidation, "validate original", err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return newConversionError(ErrArtifactValidation, "validate original", errors.New("original source is missing or empty"))
		}
	}

	for i := range manifest.Pages {
		page := &manifest.Pages[i]
		if page.Number != i+1 || page.Width < 1 || page.Height < 1 {
			return pageConversionError(ErrArtifactValidation, "validate", i+1, errors.New("invalid page geometry or numbering"))
		}
		required := map[string]bool{"page/png": false, "page/jpeg": false, "thumbnail/png": false, "thumbnail/jpeg": false}
		for _, rendition := range page.Images {
			key := rendition.Role + "/" + rendition.Format
			if _, ok := required[key]; !ok {
				continue
			}
			required[key] = true
			if err := validateRendition(dir, *page, rendition); err != nil {
				return err
			}
		}
		for key, present := range required {
			if !present {
				return pageConversionError(ErrArtifactValidation, "validate", page.Number, fmt.Errorf("missing %s rendition", key))
			}
		}
	}

	texts, err := readPageTexts(dir)
	if err != nil {
		return newConversionError(ErrArtifactValidation, "validate text.json", err)
	}
	if len(texts) != manifest.PageCount {
		return newConversionError(ErrArtifactValidation, "validate text.json", fmt.Errorf("got %d pages, want %d", len(texts), manifest.PageCount))
	}

	onDisk, err := LoadManifest(dir)
	if err != nil {
		return newConversionError(ErrArtifactValidation, "validate manifest.json", err)
	}
	if onDisk.PageCount != manifest.PageCount || len(onDisk.Pages) != len(manifest.Pages) {
		return newConversionError(ErrArtifactValidation, "validate manifest.json", errors.New("manifest differs from conversion result"))
	}
	return nil
}

func validatePDFFile(dir, name string) error {
	path, err := artifactPath(dir, name)
	if err != nil {
		return newConversionError(ErrArtifactValidation, "validate PDF", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return newConversionError(ErrArtifactValidation, "validate PDF", err)
	}
	defer f.Close()
	header := make([]byte, 5)
	if _, err := io.ReadFull(f, header); err != nil {
		return newConversionError(ErrArtifactValidation, "validate PDF", err)
	}
	if string(header) != "%PDF-" {
		return newConversionError(ErrArtifactValidation, "validate PDF", errors.New("missing PDF header"))
	}
	return nil
}

func validateRendition(dir string, page PageManifest, rendition Rendition) error {
	path, err := artifactPath(dir, rendition.Path)
	if err != nil {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() != rendition.Bytes {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, fmt.Errorf("size mismatch for %s", rendition.Path))
	}
	width, height, err := imageDimensions(path)
	if err != nil {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, err)
	}
	if width != rendition.Width || height != rendition.Height {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, fmt.Errorf("dimensions mismatch for %s", rendition.Path))
	}
	if rendition.Role == "page" && (width != page.Width || height != page.Height) {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, errors.New("page rendition differs from page geometry"))
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, err)
	}
	if !strings.EqualFold(digest, rendition.SHA256) {
		return pageConversionError(ErrArtifactValidation, "validate rendition", page.Number, fmt.Errorf("checksum mismatch for %s", rendition.Path))
	}
	return nil
}

func artifactPath(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("invalid empty or absolute artifact path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("artifact path escapes output directory")
	}
	return filepath.Join(dir, clean), nil
}

func readPageTexts(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "text.json"))
	if err != nil {
		return nil, err
	}
	var texts []string
	if err := json.Unmarshal(data, &texts); err != nil {
		return nil, err
	}
	return texts, nil
}

func writeManifest(dir string, manifest *Manifest) error {
	return writeJSONAtomic(filepath.Join(dir, "manifest.json"), manifest)
}

func writePageTexts(dir string, texts []string) error {
	return writeJSONAtomic(filepath.Join(dir, "text.json"), texts)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".json-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	return replaceFile(tmpPath, path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func imageDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	config, format, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	if format != "png" && format != "jpeg" {
		return 0, 0, fmt.Errorf("unsupported image format %q", format)
	}
	if config.Width < 1 || config.Height < 1 {
		return 0, 0, errors.New("empty image dimensions")
	}
	return config.Width, config.Height, nil
}
