package storage

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// S3Config configures an AWS S3 or S3-compatible backend. No credentials are
// persisted by this package; supply them through the application's secret
// configuration. Custom endpoints default to path-style bucket URLs.
type S3Config struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	SessionToken   string
	UseVirtualHost bool
	HTTPClient     *http.Client
}

type S3ObjectStore struct {
	endpoint       *url.URL
	region         string
	bucket         string
	accessKey      string
	secretKey      string
	sessionToken   string
	useVirtualHost bool
	client         *http.Client
	now            func() time.Time
}

func NewS3ObjectStore(config S3Config) (*S3ObjectStore, error) {
	if config.Region == "" {
		config.Region = "us-east-1"
	}
	if config.Endpoint == "" {
		config.Endpoint = "https://s3." + config.Region + ".amazonaws.com"
	}
	if config.Bucket == "" || strings.ContainsAny(config.Bucket, "/\\") {
		return nil, errors.New("invalid S3 bucket")
	}
	if config.AccessKey == "" || config.SecretKey == "" {
		return nil, errors.New("S3 access key and secret key are required")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("invalid S3 endpoint")
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &S3ObjectStore{
		endpoint:       endpoint,
		region:         config.Region,
		bucket:         config.Bucket,
		accessKey:      config.AccessKey,
		secretKey:      config.SecretKey,
		sessionToken:   config.SessionToken,
		useVirtualHost: config.UseVirtualHost,
		client:         client,
		now:            time.Now,
	}, nil
}

func (s *S3ObjectStore) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	if !safeObjectKey(key) || body == nil || size < 0 {
		return errors.New("invalid S3 object body, size, or key")
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	shaHash := sha256.New()
	md5Hash := md5.New()
	written, err := io.CopyN(io.MultiWriter(shaHash, md5Hash), &contextReader{ctx: ctx, reader: body}, size)
	if err != nil {
		return err
	}
	if written != size {
		return io.ErrUnexpectedEOF
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	payloadHash := hex.EncodeToString(shaHash.Sum(nil))
	request, err := s.newRequest(ctx, http.MethodPut, key, nil, body, size, payloadHash)
	if err != nil {
		return err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Content-Md5", base64.StdEncoding.EncodeToString(md5Hash.Sum(nil)))
	request.Header.Set("X-Amz-Meta-Sha256", payloadHash)
	s.sign(request, payloadHash)
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readS3Error(response)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
	return err
}

func (s *S3ObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !safeObjectKey(key) {
		return nil, errors.New("invalid S3 object key")
	}
	request, err := s.newRequest(ctx, http.MethodGet, key, nil, nil, 0, emptyPayloadSHA256)
	if err != nil {
		return nil, err
	}
	s.sign(request, emptyPayloadSHA256)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, readS3Error(response)
	}
	return response.Body, nil
}

func (s *S3ObjectStore) Delete(ctx context.Context, key string) error {
	if !safeObjectKey(key) {
		return errors.New("invalid S3 object key")
	}
	request, err := s.newRequest(ctx, http.MethodDelete, key, nil, nil, 0, emptyPayloadSHA256)
	if err != nil {
		return err
	}
	s.sign(request, emptyPayloadSHA256)
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readS3Error(response)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
	return err
}

func (s *S3ObjectStore) DeletePrefix(ctx context.Context, prefix string) error {
	if !strings.HasSuffix(prefix, "/") || !safeObjectKey(strings.TrimSuffix(prefix, "/")) {
		return errors.New("invalid S3 object prefix")
	}
	continuation := ""
	var keys []string
	for {
		query := url.Values{}
		query.Set("list-type", "2")
		query.Set("prefix", prefix)
		if continuation != "" {
			query.Set("continuation-token", continuation)
		}
		request, err := s.newRequest(ctx, http.MethodGet, "", query, nil, 0, emptyPayloadSHA256)
		if err != nil {
			return err
		}
		s.sign(request, emptyPayloadSHA256)
		response, err := s.client.Do(request)
		if err != nil {
			return err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			defer response.Body.Close()
			return readS3Error(response)
		}
		var listing s3ListResult
		decodeErr := xml.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&listing)
		closeErr := response.Body.Close()
		if decodeErr != nil {
			return decodeErr
		}
		if closeErr != nil {
			return closeErr
		}
		for _, object := range listing.Contents {
			if !strings.HasPrefix(object.Key, prefix) {
				return fmt.Errorf("S3 listing returned key outside requested prefix")
			}
			keys = append(keys, object.Key)
		}
		if !listing.Truncated {
			break
		}
		if listing.NextToken == "" || listing.NextToken == continuation {
			return errors.New("S3 listing did not provide a usable continuation token")
		}
		continuation = listing.NextToken
	}
	for _, key := range keys {
		if err := s.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// PublicURL returns the unsigned object URL. Use a CDN or bucket policy to
// control whether that URL is publicly readable.
func (s *S3ObjectStore) PublicURL(key string) (string, error) {
	if !safeObjectKey(key) {
		return "", errors.New("invalid S3 object key")
	}
	return s.objectURL(key).String(), nil
}

type s3ListResult struct {
	Contents []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	Truncated bool   `xml:"IsTruncated"`
	NextToken string `xml:"NextContinuationToken"`
}

// S3Error is a non-success response from the object service.
type S3Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *S3Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("S3 request failed (%d): %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("S3 request failed (%d): %s", e.StatusCode, e.Message)
}

func readS3Error(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	var parsed struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	_ = xml.Unmarshal(data, &parsed)
	if parsed.Message == "" {
		parsed.Message = strings.TrimSpace(string(data))
	}
	if parsed.Message == "" {
		parsed.Message = response.Status
	}
	return &S3Error{StatusCode: response.StatusCode, Code: parsed.Code, Message: parsed.Message}
}

func (s *S3ObjectStore) newRequest(ctx context.Context, method, key string, query url.Values, body io.Reader, size int64, payloadHash string) (*http.Request, error) {
	objectURL := s.objectURL(key)
	if query != nil {
		objectURL.RawQuery = canonicalQuery(query)
	}
	request, err := http.NewRequestWithContext(ctx, method, objectURL.String(), body)
	if err != nil {
		return nil, err
	}
	request.ContentLength = size
	now := s.now().UTC()
	request.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if s.sessionToken != "" {
		request.Header.Set("X-Amz-Security-Token", s.sessionToken)
	}
	return request, nil
}

func (s *S3ObjectStore) objectURL(key string) *url.URL {
	result := *s.endpoint
	path := strings.TrimSuffix(result.Path, "/")
	if !s.useVirtualHost {
		path += "/" + s.bucket
	} else {
		result.Host = s.bucket + "." + result.Host
	}
	if key != "" {
		path += "/" + key
	}
	if path == "" {
		path = "/"
	}
	result.Path = path
	result.RawPath = awsEncode(path, true)
	result.RawQuery = ""
	result.Fragment = ""
	return &result
}

func (s *S3ObjectStore) sign(request *http.Request, payloadHash string) {
	date := request.Header.Get("X-Amz-Date")
	shortDate := date[:8]
	host := request.URL.Host
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + date + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	if metadataHash := request.Header.Get("X-Amz-Meta-Sha256"); metadataHash != "" {
		canonicalHeaders += "x-amz-meta-sha256:" + metadataHash + "\n"
		signedHeaders += ";x-amz-meta-sha256"
	}
	if s.sessionToken != "" {
		canonicalHeaders += "x-amz-security-token:" + s.sessionToken + "\n"
		signedHeaders += ";x-amz-security-token"
	}
	canonicalURI := request.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalURI,
		canonicalQuery(request.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := shortDate + "/" + s.region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + date + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	dateKey := hmacSHA256([]byte("AWS4"+s.secretKey), shortDate)
	regionKey := hmacSHA256(dateKey, s.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.accessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		items := append([]string(nil), values[key]...)
		sort.Strings(items)
		if len(items) == 0 {
			items = []string{""}
		}
		for _, value := range items {
			parts = append(parts, awsEncode(key, false)+"="+awsEncode(value, false))
		}
	}
	return strings.Join(parts, "&")
}

func awsEncode(value string, keepSlash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~' || (keepSlash && b == '/') {
			builder.WriteByte(b)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexDigits[b>>4])
		builder.WriteByte(hexDigits[b&15])
	}
	return builder.String()
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key []byte, value string) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = io.WriteString(hash, value)
	return hash.Sum(nil)
}

func (e *S3Error) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

func (s *S3ObjectStore) String() string {
	return "S3ObjectStore(" + s.bucket + "," + strconv.Quote(s.endpoint.Redacted()) + ")"
}
