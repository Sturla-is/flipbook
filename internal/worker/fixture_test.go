package worker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/converter"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
)

func writeConversionFixture(sourcePath, outputDir string) (*converter.Result, error) {
	for _, dir := range []string{
		outputDir,
		filepath.Join(outputDir, "pages"), filepath.Join(outputDir, "thumbs"), filepath.Join(outputDir, "renditions"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"original.pdf", "converted.pdf"} {
		if err := os.WriteFile(filepath.Join(outputDir, name), source, 0644); err != nil {
			return nil, err
		}
	}
	pagePNG, err := testImage("png", 2, 3)
	if err != nil {
		return nil, err
	}
	pageJPEG, err := testImage("jpeg", 2, 3)
	if err != nil {
		return nil, err
	}
	thumbPNG, err := testImage("png", 1, 2)
	if err != nil {
		return nil, err
	}
	thumbJPEG, err := testImage("jpeg", 1, 2)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"pages/page-1.png":       pagePNG,
		"thumbs/page-1.png":      thumbPNG,
		"renditions/page-1.jpg":  pageJPEG,
		"renditions/thumb-1.jpg": thumbJPEG,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(outputDir, filepath.FromSlash(name)), data, 0644); err != nil {
			return nil, err
		}
	}
	rendition := func(role, format, path string, width, height int, data []byte) converter.Rendition {
		digest := sha256.Sum256(data)
		return converter.Rendition{
			Role: role, Format: format, Path: path, Width: width, Height: height,
			Bytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
		}
	}
	manifest := &converter.Manifest{
		SchemaVersion: converter.ManifestSchemaVersion, CreatedAt: time.Unix(100, 0).UTC(),
		SourceFormat: "pdf", SourcePDF: "converted.pdf", Original: "original.pdf",
		ConversionDPI: 300, ThumbnailDPI: 72, PageCount: 1, Width: 2, Height: 3, TextAvailable: true,
		Pages: []converter.PageManifest{{
			Number: 1, Width: 2, Height: 3, WidthPoints: 20, HeightPoints: 30, TextSource: "poppler",
			Text: []converter.TextSpan{{Text: "page text"}},
			Images: []converter.Rendition{
				rendition("page", "png", "pages/page-1.png", 2, 3, pagePNG),
				rendition("page", "jpeg", "renditions/page-1.jpg", 2, 3, pageJPEG),
				rendition("thumbnail", "png", "thumbs/page-1.png", 1, 2, thumbPNG),
				rendition("thumbnail", "jpeg", "renditions/thumb-1.jpg", 1, 2, thumbJPEG),
			},
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "manifest.json"), manifestData, 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "text.json"), []byte(`["page text"]`), 0644); err != nil {
		return nil, err
	}
	return &converter.Result{
		PageCount: 1, Width: 2, Height: 3, Pages: manifest.Pages,
		PDFPath: filepath.Join(outputDir, "converted.pdf"), ManifestPath: filepath.Join(outputDir, "manifest.json"), StagingDir: outputDir,
	}, nil
}

func testImage(format string, width, height int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 50, G: 100, B: 150, A: 255})
	var buffer bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&buffer, img)
	} else {
		err = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90})
	}
	return buffer.Bytes(), err
}

func mustWriteConversionFixture(t *testing.T, sourcePath, outputDir string) {
	t.Helper()
	if _, err := writeConversionFixture(sourcePath, outputDir); err != nil {
		t.Fatalf("write conversion fixture: %v", err)
	}
}

func writeSource(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("%PDF-1.4\nworker fixture\n")
	if err := os.WriteFile(path, contents, 0644); err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func testWorker(t *testing.T, db *fakeDB, store *storage.Storage, conv *fakeConverter) *Worker {
	t.Helper()
	options := DefaultOptions()
	options.WorkerID = "test-worker"
	options.WorkerCount = 1
	options.WebhookWorkerCount = 1
	options.HeartbeatInterval = time.Hour
	options.LeaseDuration = 2 * time.Hour
	options.PollInterval = time.Millisecond
	options.ScheduleInterval = time.Hour
	options.RetryBackoff = time.Millisecond
	options.MaxRetryBackoff = time.Second
	options.Now = func() time.Time { return time.Unix(1000, 0).UTC() }
	worker, err := newWorker(db, store, conv, options)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func runningJob(worker *Worker, job *models.Job) *models.Job {
	job.Status = models.JobRunning
	job.LeaseOwner = worker.opts.WorkerID
	job.Attempts = 1
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 3
	}
	return job
}

func addRevisionFixture(t *testing.T, store *storage.Storage, flipbookID string, number int, sourcePath string) {
	t.Helper()
	mustWriteConversionFixture(t, sourcePath, store.RevisionDir(flipbookID, number))
	if err := store.ActivateRevision(flipbookID, number); err != nil {
		t.Fatalf("activate fixture revision: %v", err)
	}
}
