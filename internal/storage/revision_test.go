package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyStorageWithoutRevisionPointer(t *testing.T) {
	store := New(t.TempDir())
	if err := store.EnsureDirs("book"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.PagesDir("book"), "page-1.png"), []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.FlipbookDir("book"), "text.json"), []byte(`["legacy text"]`), 0644); err != nil {
		t.Fatal(err)
	}
	if current, err := store.CurrentRevision("book"); err != nil || current != 0 {
		t.Fatalf("legacy current revision = %d, %v", current, err)
	}
	if got := store.PageImageURL("book", "%d", 1); got != "/data/flipbooks/book/pages/page-1.png" {
		t.Errorf("legacy URL changed: %q", got)
	}
	if !store.HasPages("book") || store.DetectPageFormat("book") != "%d" {
		t.Errorf("legacy pages were not detected")
	}
	if texts := store.LoadPageTexts("book"); len(texts) != 1 || texts[0] != "legacy text" {
		t.Errorf("legacy text was not loaded: %#v", texts)
	}
}

func TestRevisionPromotionRollbackAndVersionedURLs(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "flipbooks"))
	stagingOne, err := store.CreateRevisionStaging("book")
	if err != nil {
		t.Fatal(err)
	}
	createStorageRevision(t, stagingOne, "first")
	if err := os.WriteFile(filepath.Join(stagingOne, "text.json"), []byte(`["first"]`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteRevision("book", stagingOne, 1); err != nil {
		t.Fatal(err)
	}
	if current, err := store.CurrentRevision("book"); err != nil || current != 1 {
		t.Fatalf("current revision = %d, %v", current, err)
	}
	if got := store.PageImageURL("book", "%d", 1); got != "/data/flipbooks/book/revisions/1/pages/page-1.png" {
		t.Errorf("unexpected legacy URL facade %q", got)
	}
	if texts := store.LoadPageTexts("book"); len(texts) != 1 || texts[0] != "first" {
		t.Errorf("unexpected active text: %#v", texts)
	}

	stagingTwo, err := store.CreateRevisionStaging("book")
	if err != nil {
		t.Fatal(err)
	}
	createStorageRevision(t, stagingTwo, "second")
	if err := os.WriteFile(filepath.Join(stagingTwo, "text.json"), []byte(`["second"]`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteRevision("book", stagingTwo, 2); err != nil {
		t.Fatal(err)
	}
	if next, err := store.NextRevision("book"); err != nil || next != 3 {
		t.Errorf("next revision = %d, %v", next, err)
	}
	if err := store.ActivateRevision("book", 1); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if err := store.DeleteRevision("book", 1); !errors.Is(err, ErrRevisionActive) {
		t.Errorf("active revision deletion returned %v", err)
	}
	if err := store.DeleteRevision("book", 2); err != nil {
		t.Fatal(err)
	}

	cdn := NewWithOptions(store.BaseDir, Options{CDNBaseURL: "https://cdn.example.com/assets/"})
	if got := cdn.PageImageURL("book", "%d", 1); got != "https://cdn.example.com/assets/book/revisions/1/pages/page-1.png" {
		t.Errorf("unexpected CDN URL %q", got)
	}
	versioned, err := WithAssetVersion("/data/flipbooks/book/pages/page-1.png?download=1", 3)
	if err != nil || !strings.Contains(versioned, "v=3") || !strings.Contains(versioned, "download=1") {
		t.Errorf("unexpected versioned URL %q, %v", versioned, err)
	}
}

func TestRevisionValidationPreventsTamperedActivation(t *testing.T) {
	store := New(t.TempDir())
	staging, _ := store.CreateRevisionStaging("book")
	createStorageRevision(t, staging, "text")
	if _, err := store.PromoteRevision("book", staging, 1); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(store.RevisionDir("book", 1), "pages", "page-1.png")
	file, err := os.OpenFile(artifact, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("tamper")
	_ = file.Close()
	if err := store.ActivateRevision("book", 1); err == nil {
		t.Fatal("tampered revision was accepted")
	}
}

func TestLocalObjectSyncVerifyAndCompleteDeletion(t *testing.T) {
	remote, err := NewLocalObjectStore(filepath.Join(t.TempDir(), "objects"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewWithOptions(filepath.Join(t.TempDir(), "flipbooks"), Options{ObjectStore: remote})
	stagingOne, _ := store.CreateRevisionStaging("book")
	createStorageRevision(t, stagingOne, "text")
	if _, err := store.PromoteRevision("book", stagingOne, 1); err != nil {
		t.Fatal(err)
	}
	stagingTwo, _ := store.CreateRevisionStaging("book")
	createStorageRevision(t, stagingTwo, "text")
	if _, err := store.InstallRevision("book", stagingTwo, 2); err != nil {
		t.Fatal(err)
	}
	if current, err := store.CurrentRevision("book"); err != nil || current != 1 {
		t.Fatalf("inactive install changed current revision to %d, %v", current, err)
	}
	objects, err := store.SyncRevision(context.Background(), "book", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 8 {
		t.Errorf("synced %d objects, want 8", len(objects))
	}
	reader, err := store.OpenRevisionObject(context.Background(), "book", 2, "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateRevision("book", 2); err != nil {
		t.Fatal(err)
	}
	if current, err := store.CurrentRevision("book"); err != nil || current != 2 {
		t.Fatalf("synced revision was not activated: %d, %v", current, err)
	}
	if err := store.ActivateRevision("book", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRevisionContext(context.Background(), "book", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote.root, "book", "revisions", "2")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remote revision remains: %v", err)
	}
	if err := store.DeleteFlipbookContext(context.Background(), "book"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote.root, "book")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remote flipbook remains: %v", err)
	}
}

func createStorageRevision(t *testing.T, dir, text string) {
	t.Helper()
	for _, name := range []string{"pages", "thumbs", "renditions"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"converted.pdf", "original.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("%PDF-1.4\n%%EOF\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	images := []struct {
		role, format, path string
		width, height      int
	}{
		{"page", "png", "pages/page-1.png", 10, 20},
		{"page", "jpeg", "renditions/page-1.jpg", 10, 20},
		{"thumbnail", "png", "thumbs/page-1.png", 5, 10},
		{"thumbnail", "jpeg", "renditions/thumb-1.jpg", 5, 10},
	}
	manifestImages := make([]map[string]any, 0, len(images))
	for _, item := range images {
		path := filepath.Join(dir, filepath.FromSlash(item.path))
		writeStorageImage(t, path, item.width, item.height, item.format)
		digest, err := revisionFileSHA256(path)
		if err != nil {
			t.Fatal(err)
		}
		manifestImages = append(manifestImages, map[string]any{
			"role": item.role, "format": item.format, "path": item.path,
			"width": item.width, "height": item.height, "bytes": int64(storageFileSize(t, path)), "sha256": digest,
		})
	}
	manifest := map[string]any{
		"schema_version": 1,
		"source_pdf":     "converted.pdf",
		"original":       "original.pdf",
		"page_count":     1,
		"pages": []any{map[string]any{
			"number": 1, "width": 10, "height": 20, "images": manifestImages,
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "text.json"), []byte(fmt.Sprintf("[%q]", text)), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeStorageImage(t *testing.T, path string, width, height int, format string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 20, G: 40, B: 60, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if format == "png" {
		err = png.Encode(file, img)
	} else {
		err = jpeg.Encode(file, img, &jpeg.Options{Quality: 85})
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func storageFileSize(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return int(info.Size())
}
