package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

var (
	ErrInvalidID       = errors.New("invalid flipbook ID")
	ErrInvalidStaging  = errors.New("invalid revision staging directory")
	ErrRevisionExists  = errors.New("revision already exists")
	ErrRevisionMissing = errors.New("revision does not exist")
	ErrRevisionActive  = errors.New("cannot delete the active revision")
)

// CreateRevisionStaging creates an empty staging directory on the same
// filesystem as revisions, making the eventual rename atomic.
func (s *Storage) CreateRevisionStaging(id string) (string, error) {
	if err := validateID(id); err != nil {
		return "", err
	}
	root := s.stagingRoot(id)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "revision-*")
}

// InstallRevision validates and renames staging into an immutable revision
// without activating it. Workers can sync the installed revision to object
// storage before calling ActivateRevision.
func (s *Storage) InstallRevision(id, stagingDir string, revision int) (string, error) {
	if err := validateID(id); err != nil {
		return "", err
	}
	if revision < 1 {
		return "", fmt.Errorf("%w: revision must be positive", ErrInvalidStaging)
	}
	if err := s.validateStagingDir(id, stagingDir); err != nil {
		return "", err
	}
	if err := ValidateRevisionArtifacts(stagingDir); err != nil {
		return "", err
	}
	target := s.RevisionDir(id, revision)
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("%w: %d", ErrRevisionExists, revision)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	if err := os.Rename(stagingDir, target); err != nil {
		return "", fmt.Errorf("install revision: %w", err)
	}
	return target, nil
}

// PromoteRevision installs and then atomically activates a revision. The
// previous active revision remains untouched and can be restored with
// ActivateRevision.
func (s *Storage) PromoteRevision(id, stagingDir string, revision int) (string, error) {
	target, err := s.InstallRevision(id, stagingDir, revision)
	if err != nil {
		return "", err
	}
	if err := s.writeCurrentRevision(id, revision); err != nil {
		_ = os.RemoveAll(target)
		return "", err
	}
	return target, nil
}

// ActivateRevision validates an existing revision and atomically makes it
// active. It is also the rollback operation.
func (s *Storage) ActivateRevision(id string, revision int) error {
	if err := validateID(id); err != nil {
		return err
	}
	dir := s.RevisionDir(id, revision)
	if err := ValidateRevisionArtifacts(dir); err != nil {
		return err
	}
	return s.writeCurrentRevision(id, revision)
}

func (s *Storage) writeCurrentRevision(id string, revision int) error {
	if revision < 1 {
		return ErrRevisionMissing
	}
	dir := s.RevisionDir(id, revision)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		return fmt.Errorf("%w: %d: %v", ErrRevisionMissing, revision, err)
	}
	root := s.FlipbookDir(id)
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".current-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err := io.WriteString(file, strconv.Itoa(revision)); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replaceLocalFile(temp, filepath.Join(root, "current"))
}

// CurrentRevision returns zero for a legacy flipbook with no revision pointer.
func (s *Storage) CurrentRevision(id string) (int, error) {
	if err := validateID(id); err != nil {
		return 0, err
	}
	data, err := os.ReadFile(filepath.Join(s.FlipbookDir(id), "current"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	revision, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || revision < 1 {
		return 0, fmt.Errorf("invalid active revision pointer")
	}
	return revision, nil
}

// NextRevision returns one more than the highest immutable revision on disk.
func (s *Storage) NextRevision(id string) (int, error) {
	if err := validateID(id); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(s.revisionsRoot(id))
	if errors.Is(err, os.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	next := 1
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		revision, err := strconv.Atoi(entry.Name())
		if err == nil && revision >= next {
			next = revision + 1
		}
	}
	return next, nil
}

func (s *Storage) RevisionDir(id string, revision int) string {
	return filepath.Join(s.revisionsRoot(id), strconv.Itoa(revision))
}

func (s *Storage) RevisionPagesDir(id string, revision int) string {
	return filepath.Join(s.RevisionDir(id, revision), "pages")
}

func (s *Storage) RevisionThumbsDir(id string, revision int) string {
	return filepath.Join(s.RevisionDir(id, revision), "thumbs")
}

func (s *Storage) RevisionPDFPath(id string, revision int) string {
	return filepath.Join(s.RevisionDir(id, revision), "converted.pdf")
}

func (s *Storage) RevisionManifestPath(id string, revision int) string {
	return filepath.Join(s.RevisionDir(id, revision), "manifest.json")
}

func (s *Storage) CurrentRevisionDir(id string) (string, error) {
	revision, err := s.CurrentRevision(id)
	if err != nil {
		return "", err
	}
	if revision == 0 {
		return s.FlipbookDir(id), nil
	}
	return s.RevisionDir(id, revision), nil
}

func (s *Storage) CurrentPagesDir(id string) string {
	dir, err := s.CurrentRevisionDir(id)
	if err != nil {
		return s.PagesDir(id)
	}
	return filepath.Join(dir, "pages")
}

func (s *Storage) CurrentThumbsDir(id string) string {
	dir, err := s.CurrentRevisionDir(id)
	if err != nil {
		return s.ThumbsDir(id)
	}
	return filepath.Join(dir, "thumbs")
}

func (s *Storage) DeleteRevision(id string, revision int) error {
	return s.DeleteRevisionContext(context.Background(), id, revision)
}

func (s *Storage) DeleteRevisionContext(ctx context.Context, id string, revision int) error {
	if err := validateID(id); err != nil {
		return err
	}
	if revision < 1 {
		return fmt.Errorf("%w: %d", ErrRevisionMissing, revision)
	}
	current, err := s.CurrentRevision(id)
	if err != nil {
		return err
	}
	if current == revision {
		return ErrRevisionActive
	}
	dir := s.RevisionDir(id, revision)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %d", ErrRevisionMissing, revision)
	} else if err != nil {
		return err
	}
	if s.objects != nil {
		if err := s.objects.DeletePrefix(ctx, revisionObjectPrefix(id, revision)); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

func (s *Storage) DeleteFlipbookContext(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if s.objects != nil {
		if err := s.objects.DeletePrefix(ctx, id+"/"); err != nil {
			return err
		}
	}
	return os.RemoveAll(s.FlipbookDir(id))
}

func (s *Storage) RevisionPageImageURL(id, format string, pageNum, revision int) string {
	return s.RevisionAssetURL(id, revision, fmt.Sprintf("pages/page-"+format+".png", pageNum))
}

func (s *Storage) RevisionThumbImageURL(id, format string, pageNum, revision int) string {
	return s.RevisionAssetURL(id, revision, fmt.Sprintf("thumbs/page-"+format+".png", pageNum))
}

// RevisionAssetURL returns an immutable CDN/public URL for a revision file.
func (s *Storage) RevisionAssetURL(id string, revision int, name string) string {
	if validateID(id) != nil || revision < 1 || !safeRelativeURL(name) {
		return ""
	}
	return s.assetURL(fmt.Sprintf("%s/revisions/%d/%s", id, revision, filepath.ToSlash(name)))
}

// WithAssetVersion adds a cache-busting version to an existing asset URL.
func WithAssetVersion(rawURL string, revision int) (string, error) {
	if revision < 1 {
		return "", errors.New("asset version must be positive")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("v", strconv.Itoa(revision))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *Storage) revisionsRoot(id string) string {
	return filepath.Join(s.FlipbookDir(id), "revisions")
}

func (s *Storage) stagingRoot(id string) string {
	return filepath.Join(s.FlipbookDir(id), ".staging")
}

func (s *Storage) validateStagingDir(id, stagingDir string) error {
	if err := validateID(id); err != nil {
		return err
	}
	root, err := filepath.Abs(s.stagingRoot(id))
	if err != nil {
		return err
	}
	candidate, err := filepath.Abs(stagingDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.ContainsRune(relative, filepath.Separator) {
		return ErrInvalidStaging
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.IsDir() {
		return ErrInvalidStaging
	}
	return nil
}

func validateID(id string) error {
	if id == "" || len(id) > 128 {
		return ErrInvalidID
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return ErrInvalidID
		}
	}
	return nil
}

func safeRelativeURL(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.ContainsAny(name, "?#") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	return clean == filepath.ToSlash(name) && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/")
}

func revisionObjectPrefix(id string, revision int) string {
	return fmt.Sprintf("%s/revisions/%d/", id, revision)
}

func replaceLocalFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	} else {
		if removeErr := os.Remove(dst); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(src, dst)
	}
}

type revisionManifest struct {
	SchemaVersion int    `json:"schema_version"`
	SourcePDF     string `json:"source_pdf"`
	Original      string `json:"original"`
	PageCount     int    `json:"page_count"`
	Pages         []struct {
		Number int `json:"number"`
		Images []struct {
			Role   string `json:"role"`
			Format string `json:"format"`
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"images"`
	} `json:"pages"`
}

// ValidateRevisionArtifacts independently checks a staged or immutable
// revision before it can become active.
func ValidateRevisionArtifacts(dir string) error {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest revisionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode revision manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.PageCount < 1 || len(manifest.Pages) != manifest.PageCount {
		return errors.New("invalid revision manifest page count or schema")
	}
	if err := validateRevisionPDF(dir, manifest.SourcePDF); err != nil {
		return err
	}
	if manifest.Original != "" {
		path, err := revisionArtifactPath(dir, manifest.Original)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return errors.New("missing or invalid revision original")
		}
	}
	for i, page := range manifest.Pages {
		if page.Number != i+1 {
			return fmt.Errorf("invalid page numbering at page %d", i+1)
		}
		required := map[string]bool{"page/png": false, "page/jpeg": false, "thumbnail/png": false, "thumbnail/jpeg": false}
		for _, image := range page.Images {
			key := image.Role + "/" + image.Format
			if _, ok := required[key]; !ok {
				continue
			}
			required[key] = true
			path, err := revisionArtifactPath(dir, image.Path)
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() != image.Bytes {
				return fmt.Errorf("invalid artifact %s", image.Path)
			}
			digest, err := revisionFileSHA256(path)
			if err != nil {
				return err
			}
			if !strings.EqualFold(digest, image.SHA256) {
				return fmt.Errorf("checksum mismatch for %s", image.Path)
			}
		}
		for name, present := range required {
			if !present {
				return fmt.Errorf("page %d is missing %s", page.Number, name)
			}
		}
	}
	textData, err := os.ReadFile(filepath.Join(dir, "text.json"))
	if err != nil {
		return err
	}
	var texts []string
	if err := json.Unmarshal(textData, &texts); err != nil || len(texts) != manifest.PageCount {
		return errors.New("invalid revision text.json")
	}
	return nil
}

func validateRevisionPDF(dir, name string) error {
	path, err := revisionArtifactPath(dir, name)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 5)
	if _, err := io.ReadFull(file, header); err != nil {
		return err
	}
	if string(header) != "%PDF-" {
		return errors.New("invalid revision PDF header")
	}
	return nil
}

func revisionArtifactPath(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("invalid revision artifact path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("revision artifact escapes directory")
	}
	return filepath.Join(dir, clean), nil
}

func revisionFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
