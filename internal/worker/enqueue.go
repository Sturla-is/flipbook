package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

func (w *Worker) Enqueue(job Job) error {
	_, err := w.EnqueueContext(context.Background(), job)
	return err
}

func (w *Worker) EnqueueContext(ctx context.Context, request Job) (*models.Job, error) {
	if request.FlipbookID == "" {
		return nil, errors.New("flipbook id is required")
	}
	flipbook, err := w.db.GetFlipbook(request.FlipbookID)
	if err != nil {
		return nil, err
	}
	if request.WorkspaceID != "" && request.WorkspaceID != flipbook.WorkspaceID {
		return nil, errors.New("flipbook workspace does not match request")
	}
	revision, err := w.conversionRevision(ctx, flipbook, request.RevisionID)
	if err != nil {
		return nil, err
	}
	sourcePath := request.SourcePath
	if sourcePath == "" {
		sourcePath = w.storage.OriginalPath(flipbook.ID, fileExtension(flipbook.Filename))
	}
	source := inspectSource(sourcePath)
	maxAttempts := request.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = w.opts.MaxAttempts
	}
	key := request.IdempotencyKey
	if key == "" {
		key = "convert:" + revision.ID
	}
	job := &models.Job{
		WorkspaceID:    flipbook.WorkspaceID,
		FlipbookID:     flipbook.ID,
		RevisionID:     revision.ID,
		Type:           models.JobConvert,
		Priority:       request.Priority,
		IdempotencyKey: key,
		MaxAttempts:    maxAttempts,
		Payload: map[string]any{
			"source_path":     sourcePath,
			"source_checksum": source.SHA256,
			"source_size":     source.Size,
			"source_filename": revision.SourceFilename,
		},
	}
	if err := w.enqueueDurable(ctx, job, true); err != nil {
		return nil, err
	}
	if flipbook.Status != models.StatusReady {
		_, backupErr := w.EnqueueBackup(ctx, BackupRequest{
			WorkspaceID: flipbook.WorkspaceID, FlipbookID: flipbook.ID, RevisionID: revision.ID,
			SourcePath: sourcePath, Filename: revision.SourceFilename, SHA256: source.SHA256, Size: source.Size,
		})
		if backupErr != nil {
			return job, backupErr
		}
	}
	return job, nil
}

func (w *Worker) conversionRevision(ctx context.Context, flipbook *models.Flipbook, revisionID string) (*models.Revision, error) {
	explicit := revisionID != ""
	if revisionID == "" {
		revisionID = flipbook.CurrentRevisionID
	}
	if revisionID != "" {
		revision, err := w.db.GetRevision(ctx, flipbook.WorkspaceID, revisionID)
		if err == nil && revision.FlipbookID == flipbook.ID {
			reusable := explicit || flipbook.Status != models.StatusReady ||
				revision.Status == models.RevisionPending || revision.Status == models.RevisionConverting || revision.Status == models.RevisionFailed
			if reusable && revision.Status != models.RevisionActive && revision.Status != models.RevisionReady && revision.Status != models.RevisionSuperseded {
				return revision, nil
			}
			if explicit {
				return revision, nil
			}
		} else if err != nil {
			return nil, err
		}
	}
	revision := &models.Revision{
		WorkspaceID: flipbook.WorkspaceID, FlipbookID: flipbook.ID,
		SourceFilename: flipbook.Filename, ExternalSource: flipbook.ExternalSource,
		ExternalID: flipbook.ExternalID, CreatedBy: flipbook.OwnerID,
	}
	if err := w.db.CreateRevision(ctx, revision); err != nil {
		return nil, err
	}
	return revision, nil
}

func (w *Worker) EnqueueJob(ctx context.Context, job *models.Job) error {
	if job == nil {
		return errors.New("job is required")
	}
	supported := false
	for _, jobType := range w.opts.JobTypes {
		if job.Type == jobType {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("job type %q is not enabled for this worker", job.Type)
	}
	return w.enqueueDurable(ctx, job, true)
}

func (w *Worker) enqueueDurable(ctx context.Context, job *models.Job, retryTerminal bool) error {
	if job.MaxAttempts == 0 {
		job.MaxAttempts = w.opts.MaxAttempts
	}
	if job.RunAt.IsZero() {
		job.RunAt = w.opts.Now()
	}
	if err := w.db.EnqueueJob(ctx, job); err != nil {
		return err
	}
	if retryTerminal && (job.Status == models.JobDead || job.Status == models.JobCancelled) {
		if err := w.db.RetryJob(ctx, job.ID); err != nil {
			return err
		}
		job.Status = models.JobQueued
		job.Attempts = 0
		job.CancelRequested = false
		job.LastError = ""
		job.RunAt = w.opts.Now()
	}
	return nil
}

func (w *Worker) Cancel(ctx context.Context, jobID string) error {
	return w.db.CancelJob(ctx, jobID)
}

func (w *Worker) Retry(ctx context.Context, jobID string) error {
	return w.db.RetryJob(ctx, jobID)
}

type BackupRequest struct {
	WorkspaceID    string
	FlipbookID     string
	RevisionID     string
	SourcePath     string
	Filename       string
	SHA256         string
	Size           int64
	IdempotencyKey string
}

func (w *Worker) EnqueueBackup(ctx context.Context, request BackupRequest) (*models.Job, error) {
	if request.WorkspaceID == "" || request.FlipbookID == "" || request.SourcePath == "" {
		return nil, errors.New("backup workspace, flipbook, and source path are required")
	}
	if request.RevisionID == "" {
		flipbook, err := w.db.GetFlipbookInWorkspace(ctx, request.WorkspaceID, request.FlipbookID)
		if err != nil {
			return nil, err
		}
		request.RevisionID = flipbook.CurrentRevisionID
	}
	if request.Filename == "" {
		request.Filename = "original" + fileExtension(request.SourcePath)
	}
	key := request.IdempotencyKey
	if key == "" {
		key = "backup:" + request.RevisionID + ":" + request.SHA256
	}
	job := &models.Job{
		WorkspaceID: request.WorkspaceID, FlipbookID: request.FlipbookID, RevisionID: request.RevisionID,
		Type: models.JobBackup, IdempotencyKey: key, MaxAttempts: w.opts.MaxAttempts,
		Payload: map[string]any{
			"source_path": request.SourcePath, "source_checksum": request.SHA256,
			"source_size": request.Size, "source_filename": request.Filename,
		},
	}
	return job, w.enqueueDurable(ctx, job, true)
}

type PublicationRequest struct {
	WorkspaceID    string
	FlipbookID     string
	State          models.PublicationState
	Visibility     models.Visibility
	ScheduledFor   *time.Time
	IdempotencyKey string
}

func (w *Worker) EnqueuePublication(ctx context.Context, request PublicationRequest) (*models.Job, error) {
	var jobType models.JobType
	switch request.State {
	case models.PublicationPublished:
		jobType = models.JobPublish
	case models.PublicationUnpublished:
		jobType = models.JobUnpublish
	default:
		return nil, errors.New("only publish and unpublish jobs are supported")
	}
	if request.WorkspaceID == "" || request.FlipbookID == "" {
		return nil, errors.New("publication workspace and flipbook are required")
	}
	payload := map[string]any{"scheduled": request.ScheduledFor != nil}
	if request.Visibility != "" {
		payload["visibility"] = string(request.Visibility)
	}
	if request.ScheduledFor != nil {
		payload["scheduled_for"] = request.ScheduledFor.UTC().Format(time.RFC3339Nano)
	}
	job := &models.Job{
		WorkspaceID: request.WorkspaceID, FlipbookID: request.FlipbookID, Type: jobType,
		IdempotencyKey: request.IdempotencyKey, MaxAttempts: w.opts.MaxAttempts, Payload: payload,
	}
	if request.ScheduledFor != nil {
		job.RunAt = request.ScheduledFor.UTC()
	}
	return job, w.enqueueDurable(ctx, job, request.ScheduledFor == nil)
}

func (w *Worker) SchedulePublications(ctx context.Context) (int, error) {
	now := w.opts.Now()
	due, err := w.db.ListDuePublications(ctx, now)
	if err != nil {
		return 0, err
	}
	count := 0
	var errs []error
	for _, flipbook := range due {
		var request PublicationRequest
		switch flipbook.PublicationState {
		case models.PublicationDraft:
			if flipbook.PublishAt == nil || now.Before(*flipbook.PublishAt) || flipbook.Status != models.StatusReady ||
				(flipbook.ReviewState != models.ReviewNotRequired && flipbook.ReviewState != models.ReviewApproved) {
				continue
			}
			request = PublicationRequest{
				WorkspaceID: flipbook.WorkspaceID, FlipbookID: flipbook.ID, State: models.PublicationPublished,
				Visibility: flipbook.Visibility, ScheduledFor: flipbook.PublishAt,
				IdempotencyKey: scheduleKey(models.JobPublish, flipbook.ID, *flipbook.PublishAt),
			}
		case models.PublicationPublished:
			if flipbook.ExpiresAt == nil || now.Before(*flipbook.ExpiresAt) {
				continue
			}
			request = PublicationRequest{
				WorkspaceID: flipbook.WorkspaceID, FlipbookID: flipbook.ID, State: models.PublicationUnpublished,
				Visibility: flipbook.Visibility, ScheduledFor: flipbook.ExpiresAt,
				IdempotencyKey: scheduleKey(models.JobUnpublish, flipbook.ID, *flipbook.ExpiresAt),
			}
		default:
			continue
		}
		if _, err := w.EnqueuePublication(ctx, request); err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", flipbook.ID, err))
			continue
		}
		count++
	}
	return count, errors.Join(errs...)
}

func scheduleKey(jobType models.JobType, flipbookID string, when time.Time) string {
	return "schedule:" + string(jobType) + ":" + flipbookID + ":" + strconv.FormatInt(when.UTC().UnixNano(), 10)
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func payloadInt64(payload map[string]any, key string) int64 {
	switch value := payload[key].(type) {
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	default:
		return 0
	}
}

func fileExtension(name string) string {
	extension := strings.ToLower(filepath.Ext(filepath.Base(strings.TrimSpace(name))))
	if extension == "" {
		return ".pdf"
	}
	return extension
}
