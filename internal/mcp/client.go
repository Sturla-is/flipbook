package mcp

import (
	"bytes"
	"encoding/base64"
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

type apiRequestOptions struct {
	workspaceID    string
	idempotencyKey string
	legacyPath     string
}

type apiHTTPError struct {
	status string
	body   string
}

func (e *apiHTTPError) Error() string { return e.status + ": " + e.body }

func (s *server) apiRequest(method, path string, body interface{}, options apiRequestOptions) ([]byte, error) {
	paths := []string{"/api/v1" + path}
	if options.legacyPath != "" {
		paths = append(paths, options.legacyPath)
	}
	var lastErr error
	for index, candidate := range paths {
		var reader io.Reader
		if body != nil {
			payload, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, s.baseURL+candidate, reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		s.setRequestHeaders(req, options)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(responseBody) > 64<<20 {
			return nil, errors.New("API response exceeds 64MB")
		}
		if resp.StatusCode == http.StatusNotFound && index == 0 && len(paths) > 1 {
			lastErr = &apiHTTPError{status: resp.Status, body: string(responseBody)}
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, &apiHTTPError{status: resp.Status, body: string(responseBody)}
		}
		return responseBody, nil
	}
	return nil, lastErr
}

func (s *server) apiForm(path, legacyPath string, fields map[string]string, options apiRequestOptions) ([]byte, error) {
	paths := []string{path}
	if legacyPath != "" {
		paths = append(paths, legacyPath)
	}
	values := make(url.Values)
	for key, value := range fields {
		if value != "" {
			values.Set(key, value)
		}
	}
	for index, candidate := range paths {
		req, err := http.NewRequest(http.MethodPost, s.baseURL+candidate, strings.NewReader(values.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		s.setRequestHeaders(req, options)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusNotFound && index == 0 && len(paths) > 1 {
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, &apiHTTPError{status: resp.Status, body: string(responseBody)}
		}
		return responseBody, nil
	}
	return nil, errors.New("import endpoint not found")
}

func (s *server) setRequestHeaders(req *http.Request, options apiRequestOptions) {
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	if options.workspaceID != "" {
		req.Header.Set("X-Workspace-ID", options.workspaceID)
	}
	if options.idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", options.idempotencyKey)
	}
}

func (s *server) apiUpload(path, legacyPath, filename string, fileData []byte, fields map[string]string, options apiRequestOptions) ([]byte, error) {
	paths := []string{path}
	if legacyPath != "" {
		paths = append(paths, legacyPath)
	}
	for index, candidate := range paths {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for key, value := range fields {
			if value != "" {
				if err := writer.WriteField(key, value); err != nil {
					return nil, err
				}
			}
		}
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(fileData); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, s.baseURL+candidate, bytes.NewReader(buffer.Bytes()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		s.setRequestHeaders(req, options)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode == http.StatusNotFound && index == 0 && len(paths) > 1 {
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, &apiHTTPError{status: resp.Status, body: string(responseBody)}
		}
		return responseBody, nil
	}
	return nil, errors.New("upload endpoint not found")
}

func (s *server) pollUntilReady(id, workspaceID string) toolResult {
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	escaped := url.PathEscape(id)
	for {
		select {
		case <-timeout:
			return textError("Conversion timed out after 5 minutes. Use get_flipbook_status to continue monitoring.")
		case <-ticker.C:
			body, err := s.apiRequest(http.MethodGet, "/flipbooks/"+escaped+"/status", nil, apiRequestOptions{
				workspaceID: workspaceID, legacyPath: "/api/flipbooks/" + escaped + "/status",
			})
			if err != nil {
				continue
			}
			var status map[string]interface{}
			if json.Unmarshal(body, &status) != nil {
				continue
			}
			switch status["status"] {
			case "ready":
				details, err := s.apiRequest(http.MethodGet, "/flipbooks/"+escaped, nil, apiRequestOptions{
					workspaceID: workspaceID, legacyPath: "/api/flipbooks/" + escaped,
				})
				if err != nil {
					return jsonToolResult(body)
				}
				return jsonToolResult(details)
			case "error":
				return textError("Conversion failed: " + fmt.Sprint(status["error"]))
			}
		}
	}
}

func jsonToolResult(body []byte) toolResult {
	if len(bytes.TrimSpace(body)) == 0 {
		return textResult("Operation completed successfully.")
	}
	var structured interface{}
	if err := json.Unmarshal(body, &structured); err == nil {
		return toolResult{
			Content: []contentBlock{{Type: "text", Text: string(body)}}, StructuredContent: structured,
		}
	}
	return textResult(string(body))
}

func textResult(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}}
}

func textError(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}, IsError: true}
}

func requiredString(args map[string]interface{}, name string) (string, error) {
	value, _ := args[name].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func optionalString(args map[string]interface{}, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func requestOptions(args map[string]interface{}) apiRequestOptions {
	return apiRequestOptions{
		workspaceID: optionalString(args, "workspace_id"), idempotencyKey: optionalString(args, "idempotency_key"),
	}
}

func decodeFileArgument(args map[string]interface{}) (string, []byte, error) {
	filename, err := requiredString(args, "filename")
	if err != nil {
		return "", nil, err
	}
	encoded, err := requiredString(args, "file_base64")
	if err != nil {
		return "", nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("invalid file_base64: %w", err)
	}
	if len(data) == 0 {
		return "", nil, errors.New("file_base64 must not be empty")
	}
	return filename, data, nil
}
