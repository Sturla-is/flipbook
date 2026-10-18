package migration

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/metavert-io/flipbook/internal/converter"
	"github.com/metavert-io/flipbook/internal/models"
)

func BuildPlan(ctx context.Context, options Options) ([]Item, error) {
	if strings.TrimSpace(options.CSVFile) != "" {
		return buildCSVPlan(ctx, options)
	}
	if strings.TrimSpace(options.Folder) == "" {
		return nil, errors.New("folder or CSV input is required")
	}
	return buildFolderPlan(ctx, options)
}

func buildFolderPlan(ctx context.Context, options Options) ([]Item, error) {
	root, err := filepath.Abs(options.Folder)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("migration folder is not a directory: %s", root)
	}
	items := make([]Item, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || converter.ValidateSourceFormat(entry.Name()) != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item, err := itemFromFile(path, relative, options)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}
		item.ExternalID = localExternalID(relative)
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("no supported source documents found")
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RelativePath < items[j].RelativePath })
	assignAutomaticSlugs(items)
	return items, validatePlan(items)
}

func buildCSVPlan(ctx context.Context, options Options) ([]Item, error) {
	file, err := os.Open(options.CSVFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	indices := make(map[string]int, len(headers))
	for index, header := range headers {
		indices[strings.ToLower(strings.TrimSpace(header))] = index
	}
	root := options.Folder
	if root == "" {
		root = filepath.Dir(options.CSVFile)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0)
	for row := 2; ; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV row %d: %w", row, err)
		}
		value := func(names ...string) string {
			for _, name := range names {
				if index, ok := indices[name]; ok && index < len(record) {
					return strings.TrimSpace(record[index])
				}
			}
			return ""
		}
		source := value("path", "source_path", "file", "filename")
		if source == "" {
			return nil, fmt.Errorf("CSV row %d has no source path", row)
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(root, source)
		}
		source = filepath.Clean(source)
		relative, relErr := filepath.Rel(root, source)
		if relErr != nil || strings.HasPrefix(relative, "..") {
			relative = filepath.Base(source)
		}
		item, err := itemFromFile(source, relative, options)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: %w", row, err)
		}
		item.ExternalID = value("external_id", "issuu_id", "id")
		if item.ExternalID == "" {
			item.ExternalID = localExternalID(relative)
		}
		if title := value("title"); title != "" {
			item.Title = title
		}
		item.DesiredSlug = models.NormalizeSlug(value("desired_slug", "slug"))
		if item.DesiredSlug == "" {
			item.DesiredSlug = models.NormalizeSlug(item.Title)
		}
		item.Description = value("description")
		item.LegacyURL = value("legacy_url", "issuu_url")
		item.LegacySlug = models.NormalizeSlug(value("legacy_slug"))
		if item.LegacySlug == "" && item.LegacyURL != "" {
			item.LegacySlug, err = legacySlugFromURL(item.LegacyURL)
			if err != nil {
				return nil, fmt.Errorf("CSV row %d: %w", row, err)
			}
		}
		if language := value("language"); language != "" {
			item.Language = language
		}
		if visibility := value("visibility"); visibility != "" {
			item.Visibility = visibility
		}
		item.Tags = splitList(value("tags"))
		item.CollectionIDs = splitList(value("collection_ids", "collections"))
		if raw := value("publish"); raw != "" {
			item.Publish, err = strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("CSV row %d has invalid publish value", row)
			}
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, errors.New("CSV contains no migration rows")
	}
	assignAutomaticSlugs(items)
	return items, validatePlan(items)
}

func itemFromFile(path, relative string, options Options) (Item, error) {
	if err := converter.ValidateSourceFormat(path); err != nil {
		return Item{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Item{}, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return Item{}, errors.New("source must be a non-empty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Item{}, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return Item{}, copyErr
	}
	if closeErr != nil {
		return Item{}, closeErr
	}
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	visibility := options.Visibility
	if visibility == "" {
		visibility = string(models.VisibilityPublic)
	}
	return Item{
		SourcePath: path, RelativePath: filepath.ToSlash(relative), Title: title,
		DesiredSlug: models.NormalizeSlug(title), Language: "en", Visibility: visibility,
		Publish: options.Publish, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size(),
	}, nil
}

func localExternalID(relative string) string {
	digest := sha256.Sum256([]byte(filepath.ToSlash(relative)))
	return "local-" + hex.EncodeToString(digest[:16])
}

func legacySlugFromURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("invalid legacy URL")
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 0 {
		return "", errors.New("legacy URL has no document slug")
	}
	slug := models.NormalizeSlug(segments[len(segments)-1])
	if slug == "" || slug == "docs" {
		return "", errors.New("legacy URL has no document slug")
	}
	return slug, nil
}

func splitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '|' || r == '\n' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func assignAutomaticSlugs(items []Item) {
	used := make(map[string]int)
	for index := range items {
		base := items[index].DesiredSlug
		if base == "" {
			base = "flipbook"
		}
		used[base]++
		if used[base] > 1 {
			items[index].DesiredSlug = fmt.Sprintf("%s-%d", base, used[base])
		}
	}
}

func validatePlan(items []Item) error {
	externalIDs := make(map[string]struct{}, len(items))
	legacySlugs := make(map[string]string, len(items))
	for _, item := range items {
		if item.ExternalID == "" || item.DesiredSlug == "" {
			return errors.New("every migration item requires an external ID and desired slug")
		}
		if _, ok := externalIDs[item.ExternalID]; ok {
			return fmt.Errorf("duplicate external ID %q", item.ExternalID)
		}
		externalIDs[item.ExternalID] = struct{}{}
		if item.LegacySlug != "" && item.LegacySlug != item.DesiredSlug {
			if other, ok := legacySlugs[item.LegacySlug]; ok && other != item.ExternalID {
				return fmt.Errorf("legacy slug %q is assigned to multiple documents", item.LegacySlug)
			}
			legacySlugs[item.LegacySlug] = item.ExternalID
		}
	}
	return nil
}
