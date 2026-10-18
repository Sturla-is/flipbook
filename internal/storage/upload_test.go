package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSaveUploadStreamLimitDigestAndAtomicReplacement(t *testing.T) {
	store := New(t.TempDir())
	content := "streamed document"
	info, err := store.SaveUploadStream(context.Background(), "book", "File.PDF", strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	if info.Size != int64(len(content)) || info.SHA256 != hex.EncodeToString(digest[:]) || info.Extension != ".pdf" {
		t.Errorf("unexpected upload info: %+v", info)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil || string(data) != content {
		t.Errorf("stored upload = %q, %v", data, err)
	}

	_, err = store.SaveUploadStream(context.Background(), "book", "replacement.pdf", strings.NewReader("too large"), 4)
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("oversize upload returned %v", err)
	}
	data, readErr := os.ReadFile(info.Path)
	if readErr != nil || string(data) != content {
		t.Errorf("failed replacement changed previous upload: %q, %v", data, readErr)
	}
}

func TestSaveStagedUploadAndCancellation(t *testing.T) {
	store := New(t.TempDir())
	staging, err := store.CreateRevisionStaging("book")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.SaveStagedUpload(ctx, "book", staging, "source.docx", strings.NewReader("content"), 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upload returned %v", err)
	}
	info, err := store.SaveStagedUpload(context.Background(), "book", staging, "source.docx", strings.NewReader("content"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != staging+string(os.PathSeparator)+"original.docx" {
		t.Errorf("unexpected staged path %q", info.Path)
	}
	if _, err := store.SaveStagedUpload(context.Background(), "book", t.TempDir(), "source.docx", strings.NewReader("content"), 100); !errors.Is(err, ErrInvalidStaging) {
		t.Errorf("outside staging path returned %v", err)
	}
}
