package integrations

import (
	"context"
	"net/http"

	"github.com/jonradoff/flipbook/internal/models"
)

type canvaExportResponse struct {
	Job struct {
		ID     string   `json:"id"`
		Status string   `json:"status"`
		URLs   []string `json:"urls"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"job"`
}

func (r canvaExportResponse) exportJob() *exportJob {
	job := &exportJob{
		ID: r.Job.ID, Status: r.Job.Status, DownloadURLs: append([]string(nil), r.Job.URLs...),
		ErrorCode: r.Job.Error.Code, ErrorMessage: r.Job.Error.Message,
	}
	if job.Status == "" {
		job.Status = "in_progress"
	}
	return job
}

func (c *providerClient) createCanvaExport(ctx context.Context, connection *models.IntegrationConnection, externalID string) (*exportJob, error) {
	input := map[string]any{
		"design_id": externalID,
		"format": map[string]any{
			"type": "pdf", "export_quality": "regular",
		},
	}
	var response canvaExportResponse
	if err := c.doJSON(ctx, connection, http.MethodPost, c.config.ExportPath, "export", input, &response); err != nil {
		return nil, err
	}
	return response.exportJob(), nil
}

func (c *providerClient) canvaExportStatus(ctx context.Context, connection *models.IntegrationConnection, job *exportJob) (*exportJob, error) {
	reference := job.StatusURL
	if reference == "" {
		reference = expandProviderPath(c.config.ExportStatusPath, "jobID", job.ID)
	}
	var response canvaExportResponse
	if err := c.doJSON(ctx, connection, http.MethodGet, reference, "export status", nil, &response); err != nil {
		return nil, err
	}
	updated := response.exportJob()
	updated.StatusURL = job.StatusURL
	return updated, nil
}
