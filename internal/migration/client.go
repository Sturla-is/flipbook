package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type APIClient struct {
	BaseURL     string
	APIKey      string
	WorkspaceID string
	HTTPClient  *http.Client
}

func NewAPIClient(baseURL, apiKey, workspaceID string) *APIClient {
	return &APIClient{
		BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, WorkspaceID: workspaceID,
		HTTPClient: &http.Client{Timeout: 30 * time.Minute},
	}
}

func (c *APIClient) endpoint(path string) string {
	if strings.HasSuffix(c.BaseURL, "/api/v1") {
		return c.BaseURL + path
	}
	return c.BaseURL + "/api/v1" + path
}

func (c *APIClient) Create(ctx context.Context, item Item, initialSlug string, source io.Reader) (UploadResult, error) {
	fields := map[string]string{
		"title": item.Title, "slug": initialSlug, "description": item.Description,
		"language": item.Language, "visibility": item.Visibility,
		"external_source": "issuu", "external_id": item.ExternalID,
		"tags": strings.Join(item.Tags, ","), "collection_ids": strings.Join(item.CollectionIDs, ","),
	}
	body, replay, err := c.upload(ctx, "/flipbooks", item.SourcePath, fields, source, item.ExternalID)
	if err != nil {
		return UploadResult{}, err
	}
	var response struct {
		ID         string    `json:"id"`
		Slug       string    `json:"slug"`
		Status     string    `json:"status"`
		JobID      string    `json:"job_id"`
		RevisionID string    `json:"revision_id"`
		Flipbook   *Flipbook `json:"flipbook"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return UploadResult{}, fmt.Errorf("decode create response: %w", err)
	}
	result := UploadResult{JobID: response.JobID, RevisionID: response.RevisionID, Replay: replay}
	if response.Flipbook != nil {
		result.Flipbook = *response.Flipbook
	} else {
		result.Flipbook = Flipbook{ID: response.ID, Slug: response.Slug, Status: response.Status, CurrentRevisionID: response.RevisionID}
	}
	if result.Flipbook.ID == "" {
		return UploadResult{}, errors.New("create response has no flipbook ID")
	}
	return result, nil
}

func (c *APIClient) Replace(ctx context.Context, flipbookID string, item Item, source io.Reader) (UploadResult, error) {
	body, _, err := c.upload(ctx, "/flipbooks/"+url.PathEscape(flipbookID)+"/replace", item.SourcePath, nil, source, item.ExternalID)
	if err != nil {
		return UploadResult{}, err
	}
	var response struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
		Revision struct {
			ID string `json:"id"`
		} `json:"revision"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return UploadResult{}, fmt.Errorf("decode replace response: %w", err)
	}
	return UploadResult{Flipbook: Flipbook{ID: flipbookID, Status: "pending", CurrentRevisionID: response.Revision.ID}, JobID: response.Job.ID, RevisionID: response.Revision.ID}, nil
}

func (c *APIClient) Status(ctx context.Context, flipbookID string) (Flipbook, error) {
	var result Flipbook
	err := c.doJSON(ctx, http.MethodGet, "/flipbooks/"+url.PathEscape(flipbookID)+"/status", nil, &result, "")
	return result, err
}

func (c *APIClient) ChangeSlug(ctx context.Context, flipbookID, desiredSlug string) (Flipbook, error) {
	var result Flipbook
	err := c.doJSON(ctx, http.MethodPatch, "/flipbooks/"+url.PathEscape(flipbookID), map[string]string{"slug": desiredSlug}, &result, "slug:"+flipbookID+":"+desiredSlug)
	return result, err
}

func (c *APIClient) Publish(ctx context.Context, flipbookID, visibility string) (Flipbook, error) {
	var result Flipbook
	payload := map[string]interface{}{}
	if visibility != "" {
		payload["visibility"] = visibility
	}
	err := c.doJSON(ctx, http.MethodPost, "/flipbooks/"+url.PathEscape(flipbookID)+"/publish", payload, &result, "publish:"+flipbookID)
	return result, err
}

func (c *APIClient) doJSON(ctx context.Context, method, path string, payload interface{}, destination interface{}, idempotencyKey string) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	c.setHeaders(request, idempotencyKey)
	response, err := c.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil {
		return err
	}
	if response.StatusCode >= 400 {
		return apiError(response.Status, responseBody)
	}
	if destination != nil && len(bytes.TrimSpace(responseBody)) > 0 {
		if err := json.Unmarshal(responseBody, destination); err != nil {
			return fmt.Errorf("decode API response: %w", err)
		}
	}
	return nil
}

func (c *APIClient) upload(ctx context.Context, path, filename string, fields map[string]string, source io.Reader, idempotencyKey string) ([]byte, bool, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	writeErr := make(chan error, 1)
	go func() {
		defer close(writeErr)
		for key, value := range fields {
			if value == "" {
				continue
			}
			if err := multipartWriter.WriteField(key, value); err != nil {
				_ = writer.CloseWithError(err)
				writeErr <- err
				return
			}
		}
		part, err := multipartWriter.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, source)
		}
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		_ = writer.CloseWithError(err)
		writeErr <- err
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path), reader)
	if err != nil {
		_ = reader.CloseWithError(err)
		return nil, false, err
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	c.setHeaders(request, idempotencyKey)
	response, requestErr := c.httpClient().Do(request)
	if requestErr != nil {
		_ = reader.CloseWithError(requestErr)
		<-writeErr
		return nil, false, requestErr
	}
	_ = reader.Close()
	if err := <-writeErr; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		_ = response.Body.Close()
		return nil, false, fmt.Errorf("stream upload: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if err != nil {
		return nil, false, err
	}
	replay := response.Header.Get("Idempotent-Replay") == "true"
	if response.StatusCode >= 400 {
		return nil, false, apiError(response.Status, body)
	}
	return body, replay, nil
}

func (c *APIClient) setHeaders(request *http.Request, idempotencyKey string) {
	if c.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.WorkspaceID != "" {
		request.Header.Set("X-Workspace-ID", c.WorkspaceID)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
}

func (c *APIClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func apiError(status string, body []byte) error {
	const maximum = 64 * 1024
	if len(body) > maximum {
		body = body[:maximum]
	}
	return fmt.Errorf("API %s: %s", status, strings.TrimSpace(string(body)))
}
