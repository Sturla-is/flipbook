package integrations

import (
	"context"
	"net/http"
	"strings"

	"github.com/jonradoff/flipbook/internal/models"
)

type adobeRendition struct {
	RenditionURL string `json:"renditionUrl"`
	URL          string `json:"url"`
	PageNumber   int    `json:"pageNumber"`
}

type adobeExportResponse struct {
	JobID                string           `json:"jobId"`
	Status               string           `json:"status"`
	StatusURL            string           `json:"statusUrl"`
	RenditionURL         string           `json:"renditionUrl"`
	PageRenditionsResult []adobeRendition `json:"pageRenditionsResult"`
	Renditions           []adobeRendition `json:"renditions"`
	URLs                 []string         `json:"urls"`
	Error                struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Description string `json:"description"`
	} `json:"error"`
}

func (r adobeExportResponse) exportJob() *exportJob {
	job := &exportJob{
		ID: r.JobID, Status: r.Status, StatusURL: r.StatusURL,
		ErrorCode: r.Error.Code, ErrorMessage: r.Error.Message,
	}
	if job.ErrorMessage == "" {
		job.ErrorMessage = r.Error.Description
	}
	job.DownloadURLs = append(job.DownloadURLs, r.URLs...)
	if r.RenditionURL != "" {
		job.DownloadURLs = append(job.DownloadURLs, r.RenditionURL)
	}
	for _, rendition := range append(r.PageRenditionsResult, r.Renditions...) {
		switch {
		case rendition.RenditionURL != "":
			job.DownloadURLs = append(job.DownloadURLs, rendition.RenditionURL)
		case rendition.URL != "":
			job.DownloadURLs = append(job.DownloadURLs, rendition.URL)
		}
	}
	job.DownloadURLs = uniqueStrings(job.DownloadURLs)
	if job.Status == "" {
		job.Status = "in_progress"
	}
	return job
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (c *providerClient) createAdobeExport(ctx context.Context, connection *models.IntegrationConnection, externalID, pages string) (*exportJob, error) {
	input := map[string]any{
		"id": externalID,
		"options": map[string]any{
			"format": "application/pdf", "pdfType": "standard", "downloadIndividualPdfFiles": false,
		},
	}
	if pages != "" {
		input["pages"] = pages
	}
	var response adobeExportResponse
	if err := c.doJSON(ctx, connection, http.MethodPost, c.config.ExportPath, "export", input, &response); err != nil {
		return nil, err
	}
	return response.exportJob(), nil
}

func (c *providerClient) adobeExportStatus(ctx context.Context, connection *models.IntegrationConnection, job *exportJob) (*exportJob, error) {
	reference := job.StatusURL
	if reference == "" {
		reference = expandProviderPath(c.config.ExportStatusPath, "jobID", job.ID)
	}
	var response adobeExportResponse
	if err := c.doJSON(ctx, connection, http.MethodGet, reference, "export status", nil, &response); err != nil {
		return nil, err
	}
	updated := response.exportJob()
	if updated.ID == "" {
		updated.ID = job.ID
	}
	if updated.StatusURL == "" {
		updated.StatusURL = job.StatusURL
	}
	return updated, nil
}
