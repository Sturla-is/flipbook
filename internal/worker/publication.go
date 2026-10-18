package worker

import (
	"context"
	"errors"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
)

func (w *Worker) processPublication(ctx context.Context, job *models.Job, progress *progressState) error {
	flipbook, err := w.db.GetFlipbookInWorkspace(ctx, job.WorkspaceID, job.FlipbookID)
	if err != nil {
		return err
	}
	now := w.opts.Now()
	scheduled := payloadBool(job.Payload, "scheduled")
	visibility := models.Visibility(payloadString(job.Payload, "visibility"))
	if visibility == "" {
		visibility = flipbook.Visibility
	}
	if visibility == "" {
		visibility = models.VisibilityPublic
	}

	switch job.Type {
	case models.JobPublish:
		if flipbook.PublicationState == models.PublicationPublished {
			progress.Set(100, "already published")
			return nil
		}
		if scheduled && !scheduleIsDue(flipbook.PublishAt, job.Payload["scheduled_for"], now) {
			progress.Set(100, "publish schedule changed")
			return nil
		}
		if flipbook.Status != models.StatusReady {
			return errors.New("publication is not ready")
		}
		if flipbook.ReviewState != models.ReviewNotRequired && flipbook.ReviewState != models.ReviewApproved {
			return errors.New("publication requires review approval")
		}
		progress.Set(50, "publishing")
		if err := w.db.SetPublicationState(ctx, flipbook.WorkspaceID, flipbook.ID, models.PublicationPublished, visibility, now); err != nil {
			return err
		}
		progress.Set(100, "published")
		return nil
	case models.JobUnpublish:
		if flipbook.PublicationState != models.PublicationPublished {
			progress.Set(100, "already unpublished")
			return nil
		}
		if scheduled && !scheduleIsDue(flipbook.ExpiresAt, job.Payload["scheduled_for"], now) {
			progress.Set(100, "expiry schedule changed")
			return nil
		}
		progress.Set(50, "unpublishing")
		if err := w.db.SetPublicationState(ctx, flipbook.WorkspaceID, flipbook.ID, models.PublicationUnpublished, visibility, now); err != nil {
			return err
		}
		progress.Set(100, "unpublished")
		return nil
	default:
		return errors.New("unsupported publication job")
	}
}

func scheduleIsDue(scheduled *time.Time, payload any, now time.Time) bool {
	if scheduled == nil || now.Before(*scheduled) {
		return false
	}
	encoded, ok := payload.(string)
	if !ok || encoded == "" {
		return true
	}
	expected, err := time.Parse(time.RFC3339Nano, encoded)
	if err != nil {
		return false
	}
	return scheduled.UTC().Equal(expected.UTC())
}
