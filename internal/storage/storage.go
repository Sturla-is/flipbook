package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Options configures public URLs and the optional remote object backend. The
// zero value keeps the existing local /data/flipbooks behavior.
type Options struct {
	PublicBaseURL string
	CDNBaseURL    string
	ObjectStore   ObjectStore
}

type Storage struct {
	BaseDir       string
	publicBaseURL string
	cdnBaseURL    string
	objects       ObjectStore
}

func New(baseDir string) *Storage {
	return NewWithOptions(baseDir, Options{})
}

func NewWithOptions(baseDir string, options Options) *Storage {
	_ = os.MkdirAll(baseDir, 0755)
	if options.PublicBaseURL == "" {
		options.PublicBaseURL = "/data/flipbooks"
	}
	return &Storage{
		BaseDir:       baseDir,
		publicBaseURL: strings.TrimRight(options.PublicBaseURL, "/"),
		cdnBaseURL:    strings.TrimRight(options.CDNBaseURL, "/"),
		objects:       options.ObjectStore,
	}
}

func (s *Storage) FlipbookDir(id string) string {
	return filepath.Join(s.BaseDir, id)
}

// PagesDir returns the legacy live directory. Revision-aware workers should
// use RevisionPagesDir or CurrentPagesDir instead.
func (s *Storage) PagesDir(id string) string {
	return filepath.Join(s.BaseDir, id, "pages")
}

func (s *Storage) ThumbsDir(id string) string {
	return filepath.Join(s.BaseDir, id, "thumbs")
}

func (s *Storage) OriginalPath(id, ext string) string {
	return filepath.Join(s.BaseDir, id, "original"+ext)
}

func (s *Storage) PDFPath(id string) string {
	return filepath.Join(s.BaseDir, id, "converted.pdf")
}

// SaveUpload streams an upload to disk without a size limit. New HTTP callers
// should use SaveUploadWithLimit or SaveUploadStream.
func (s *Storage) SaveUpload(id, filename string, r io.Reader) (string, error) {
	return s.SaveUploadWithLimit(id, filename, r, 0)
}

func (s *Storage) EnsureDirs(id string) error {
	if err := os.MkdirAll(s.PagesDir(id), 0755); err != nil {
		return err
	}
	return os.MkdirAll(s.ThumbsDir(id), 0755)
}

func (s *Storage) DeleteFlipbook(id string) error {
	return s.DeleteFlipbookContext(context.Background(), id)
}

// DetectPageFormat checks the active revision (or legacy output) to determine
// the page filename pattern used by existing viewers.
func (s *Storage) DetectPageFormat(id string) string {
	pagesDir := s.CurrentPagesDir(id)
	for _, pattern := range []string{"page-1.png", "page-01.png", "page-001.png", "page-0001.png"} {
		if _, err := os.Stat(filepath.Join(pagesDir, pattern)); err == nil {
			switch pattern {
			case "page-1.png":
				return "%d"
			case "page-01.png":
				return "%02d"
			case "page-001.png":
				return "%03d"
			case "page-0001.png":
				return "%04d"
			}
		}
	}
	return "%d"
}

func (s *Storage) PageImageURL(id, format string, pageNum int) string {
	if revision, err := s.CurrentRevision(id); err == nil && revision > 0 {
		return s.RevisionPageImageURL(id, format, pageNum, revision)
	}
	return s.assetURL(fmt.Sprintf("%s/pages/page-"+format+".png", id, pageNum))
}

func (s *Storage) ThumbImageURL(id, format string, pageNum int) string {
	if revision, err := s.CurrentRevision(id); err == nil && revision > 0 {
		return s.RevisionThumbImageURL(id, format, pageNum, revision)
	}
	return s.assetURL(fmt.Sprintf("%s/thumbs/page-"+format+".png", id, pageNum))
}

// HasPages checks if any converted page images exist in the active revision or
// legacy output.
func (s *Storage) HasPages(id string) bool {
	matches, _ := filepath.Glob(filepath.Join(s.CurrentPagesDir(id), "page-*.png"))
	return len(matches) > 0
}

// LoadPageTexts reads text.json for the active revision. It returns nil when
// no valid text artifact exists, preserving the original API contract.
func (s *Storage) LoadPageTexts(id string) []string {
	dir, err := s.CurrentRevisionDir(id)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "text.json"))
	if err != nil {
		return nil
	}
	var texts []string
	if err := json.Unmarshal(data, &texts); err != nil {
		return nil
	}
	return texts
}

func (s *Storage) assetURL(name string) string {
	base := s.cdnBaseURL
	if base == "" {
		base = s.publicBaseURL
	}
	if base == "" {
		base = "/data/flipbooks"
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(filepath.ToSlash(name), "/")
}
