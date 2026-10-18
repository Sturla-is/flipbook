package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3CompatibleStoreRoundTripAndPrefixDeletion(t *testing.T) {
	emulator := newS3Emulator(t)
	defer emulator.Close()
	store, err := NewS3ObjectStore(S3Config{
		Endpoint:   emulator.URL,
		Region:     "us-east-1",
		Bucket:     "bucket",
		AccessKey:  "test-access",
		SecretKey:  "test-secret",
		HTTPClient: emulator.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC) }
	ctx := context.Background()
	content := []byte("remote revision content")
	key := "book/revisions/1/file name+.txt"
	if err := store.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if err := VerifyObject(ctx, store, ObjectInfo{Key: key, Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		data := []byte(name)
		if err := store.Put(ctx, "book/revisions/1/"+name, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Put(ctx, "book/revisions/2/keep.txt", bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrefix(ctx, "book/revisions/1/"); err != nil {
		t.Fatal(err)
	}
	emulator.mu.Lock()
	defer emulator.mu.Unlock()
	if len(emulator.objects) != 1 {
		t.Errorf("objects remaining after prefix deletion: %#v", emulator.objects)
	}
	if _, ok := emulator.objects["book/revisions/2/keep.txt"]; !ok {
		t.Errorf("unrelated revision was deleted")
	}
}

func TestAWSEncoding(t *testing.T) {
	if got := awsEncode("a b+c/~", true); got != "a%20b%2Bc/~" {
		t.Errorf("unexpected path encoding %q", got)
	}
	if got := awsEncode("a b+c/~", false); got != "a%20b%2Bc%2F~" {
		t.Errorf("unexpected query encoding %q", got)
	}
}

type s3Emulator struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
	t       *testing.T
}

func newS3Emulator(t *testing.T) *s3Emulator {
	emulator := &s3Emulator{objects: make(map[string][]byte), t: t}
	emulator.Server = httptest.NewServer(http.HandlerFunc(emulator.serveHTTP))
	return emulator
}

func (s *s3Emulator) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-access/") {
		http.Error(w, "missing signature", http.StatusForbidden)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		s.list(w, r.URL.Query())
		return
	}
	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		digest := sha256.Sum256(body)
		if r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(digest[:]) {
			http.Error(w, "payload digest mismatch", 400)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), "x-amz-meta-sha256") {
			http.Error(w, "metadata header not signed", 400)
			return
		}
		s.objects[key] = body
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		body, ok := s.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	case http.MethodDelete:
		delete(s.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *s3Emulator) list(w http.ResponseWriter, query url.Values) {
	prefix := query.Get("prefix")
	var keys []string
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	start, _ := strconv.Atoi(query.Get("continuation-token"))
	if start > len(keys) {
		start = len(keys)
	}
	end := start + 2
	if end > len(keys) {
		end = len(keys)
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, "<ListBucketResult>")
	for _, key := range keys[start:end] {
		_, _ = io.WriteString(w, "<Contents><Key>")
		_ = xml.EscapeText(w, []byte(key))
		_, _ = io.WriteString(w, "</Key></Contents>")
	}
	_, _ = io.WriteString(w, fmt.Sprintf("<IsTruncated>%t</IsTruncated>", end < len(keys)))
	if end < len(keys) {
		_, _ = io.WriteString(w, fmt.Sprintf("<NextContinuationToken>%d</NextContinuationToken>", end))
	}
	_, _ = io.WriteString(w, "</ListBucketResult>")
}
