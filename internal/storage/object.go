package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// ObjectStore is the optional remote rendition/original backend. DeletePrefix
// accepts hierarchical prefixes ending in '/'; implementations must be
// idempotent so failed syncs and deletions can be retried safely.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
}

// ObjectInfo identifies an uploaded object and its expected content digest.
type ObjectInfo struct {
	Key         string
	Size        int64
	SHA256      string
	ContentType string
}

// LocalObjectStore is the default object backend and is also useful for tests.
type LocalObjectStore struct {
	root string
}

func NewLocalObjectStore(root string) (*LocalObjectStore, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0755); err != nil {
		return nil, err
	}
	return &LocalObjectStore{root: absolute}, nil
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	if body == nil || size < 0 {
		return errors.New("invalid object body or size")
	}
	path, err := s.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".object-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	reader := io.Reader(&contextReader{ctx: ctx, reader: body})
	written, err := io.CopyN(file, reader, size)
	if err == nil && written != size {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceLocalFile(temp, path)
}

func (s *LocalObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.objectPath(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *LocalObjectStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *LocalObjectStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !strings.HasSuffix(prefix, "/") {
		return errors.New("object prefix must end in '/'")
	}
	path, err := s.objectPath(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return err
	}
	if path == s.root {
		return errors.New("refusing to delete object store root")
	}
	return os.RemoveAll(path)
}

func (s *LocalObjectStore) objectPath(key string) (string, error) {
	if !safeObjectKey(key) {
		return "", errors.New("invalid object key")
	}
	path := filepath.Join(s.root, filepath.FromSlash(key))
	relative, err := filepath.Rel(s.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("object key escapes store")
	}
	return path, nil
}

func safeObjectKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// PutFile uploads one local file and returns its size and SHA-256 digest.
func PutFile(ctx context.Context, store ObjectStore, key, path string) (ObjectInfo, error) {
	if store == nil {
		return ObjectInfo{}, errors.New("object storage is not configured")
	}
	if !safeObjectKey(key) {
		return ObjectInfo{}, errors.New("invalid object key")
	}
	file, err := os.Open(path)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return ObjectInfo{}, err
	}
	if !stat.Mode().IsRegular() {
		return ObjectInfo{}, errors.New("object source is not a regular file")
	}
	digest, err := revisionFileSHA256(path)
	if err != nil {
		return ObjectInfo{}, err
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := store.Put(ctx, key, file, stat.Size(), contentType); err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Key: key, Size: stat.Size(), SHA256: digest, ContentType: contentType}, nil
}

// VerifyObject downloads an object and checks its exact size and SHA-256 sum.
func VerifyObject(ctx context.Context, store ObjectStore, expected ObjectInfo) error {
	if store == nil {
		return errors.New("object storage is not configured")
	}
	reader, err := store.Open(ctx, expected.Key)
	if err != nil {
		return err
	}
	defer reader.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, &contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return err
	}
	if written != expected.Size {
		return fmt.Errorf("object %s size mismatch: got %d, want %d", expected.Key, written, expected.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected.SHA256) {
		return fmt.Errorf("object %s checksum mismatch", expected.Key)
	}
	return nil
}

// SyncRevision uploads every immutable revision file. A true verify flag reads
// each object back and checks its size and SHA-256 sum.
func (s *Storage) SyncRevision(ctx context.Context, id string, revision int, verify bool) ([]ObjectInfo, error) {
	if s.objects == nil {
		return nil, nil
	}
	if err := validateID(id); err != nil || revision < 1 {
		return nil, errors.New("invalid flipbook ID or revision")
	}
	dir := s.RevisionDir(id, revision)
	if err := ValidateRevisionArtifacts(dir); err != nil {
		return nil, err
	}
	prefix := revisionObjectPrefix(id, revision)
	var uploaded []ObjectInfo
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("revision contains non-regular file %s", path)
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		object, err := PutFile(ctx, s.objects, prefix+filepath.ToSlash(relative), path)
		if err != nil {
			return err
		}
		if verify {
			if err := VerifyObject(ctx, s.objects, object); err != nil {
				return err
			}
		}
		uploaded = append(uploaded, object)
		return ctx.Err()
	})
	return uploaded, err
}

var ErrObjectStorageDisabled = errors.New("object storage is not configured")

func (s *Storage) ObjectStorageEnabled() bool {
	return s.objects != nil
}

// OpenRevisionObject opens a remote immutable revision asset for authorized
// handlers. Local storage remains the default when no backend is configured.
func (s *Storage) OpenRevisionObject(ctx context.Context, id string, revision int, name string) (io.ReadCloser, error) {
	if s.objects == nil {
		return nil, ErrObjectStorageDisabled
	}
	if validateID(id) != nil || revision < 1 || !safeRelativeURL(name) {
		return nil, errors.New("invalid flipbook ID, revision, or object name")
	}
	return s.objects.Open(ctx, revisionObjectPrefix(id, revision)+filepath.ToSlash(name))
}
