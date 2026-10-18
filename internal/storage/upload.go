package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrUploadTooLarge = errors.New("upload exceeds size limit")
	ErrInvalidUpload  = errors.New("invalid upload")
)

// UploadInfo reports the verified size and digest of a streamed upload.
type UploadInfo struct {
	Path      string
	Size      int64
	SHA256    string
	Extension string
}

// SaveUploadWithLimit preserves the original path-returning API while adding
// an optional byte limit. A zero limit means unlimited.
func (s *Storage) SaveUploadWithLimit(id, filename string, r io.Reader, maxBytes int64) (string, error) {
	info, err := s.SaveUploadStream(context.Background(), id, filename, r, maxBytes)
	return info.Path, err
}

// SaveUploadStream atomically writes an upload while computing its digest. It
// never holds the uploaded document in memory.
func (s *Storage) SaveUploadStream(ctx context.Context, id, filename string, r io.Reader, maxBytes int64) (UploadInfo, error) {
	if err := validateID(id); err != nil {
		return UploadInfo{}, err
	}
	return s.saveUpload(ctx, s.FlipbookDir(id), filename, r, maxBytes)
}

// SaveStagedUpload retains a revision source in a staging directory created by
// CreateRevisionStaging.
func (s *Storage) SaveStagedUpload(ctx context.Context, id, stagingDir, filename string, r io.Reader, maxBytes int64) (UploadInfo, error) {
	if err := validateID(id); err != nil {
		return UploadInfo{}, err
	}
	if err := s.validateStagingDir(id, stagingDir); err != nil {
		return UploadInfo{}, err
	}
	return s.saveUpload(ctx, stagingDir, filename, r, maxBytes)
}

func (s *Storage) saveUpload(ctx context.Context, dir, filename string, r io.Reader, maxBytes int64) (UploadInfo, error) {
	if r == nil {
		return UploadInfo{}, fmt.Errorf("%w: nil reader", ErrInvalidUpload)
	}
	if maxBytes < 0 {
		return UploadInfo{}, fmt.Errorf("%w: negative size limit", ErrInvalidUpload)
	}
	extension, err := uploadExtension(filename)
	if err != nil {
		return UploadInfo{}, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return UploadInfo{}, fmt.Errorf("create upload directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return UploadInfo{}, fmt.Errorf("create upload: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	hash := sha256.New()
	reader := io.Reader(&contextReader{ctx: ctx, reader: r})
	if maxBytes > 0 && maxBytes < math.MaxInt64 {
		reader = io.LimitReader(reader, maxBytes+1)
	}
	written, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return UploadInfo{}, fmt.Errorf("write upload: %w", copyErr)
	}
	if closeErr != nil {
		return UploadInfo{}, closeErr
	}
	if maxBytes > 0 && written > maxBytes {
		return UploadInfo{}, fmt.Errorf("%w: %d bytes exceeds %d", ErrUploadTooLarge, written, maxBytes)
	}
	destination := filepath.Join(dir, "original"+extension)
	if err := os.Chmod(tempPath, 0644); err != nil {
		return UploadInfo{}, err
	}
	if err := replaceLocalFile(tempPath, destination); err != nil {
		return UploadInfo{}, fmt.Errorf("install upload: %w", err)
	}
	return UploadInfo{
		Path:      destination,
		Size:      written,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		Extension: extension,
	}, nil
}

func uploadExtension(filename string) (string, error) {
	extension := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	if len(extension) < 2 || len(extension) > 12 {
		return "", fmt.Errorf("%w: missing or invalid file extension", ErrInvalidUpload)
	}
	for _, r := range extension[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", fmt.Errorf("%w: unsafe file extension %q", ErrInvalidUpload, extension)
		}
	}
	return extension, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}
