package publishing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/crypto/bcrypt"
)

func (s *Service) SubmitForReview(ctx context.Context, actor Actor, workspaceID, flipbookID, note string) (*models.Flipbook, error) {
	fb, workspace, err := s.reviewContext(ctx, actor, workspaceID, flipbookID, models.RoleEditor)
	if err != nil {
		return nil, err
	}
	if !workspace.Settings.RequirePublicationReview {
		return nil, invalid("publication review is not required in this workspace")
	}
	if fb.Status != models.StatusReady || fb.PublicationState == models.PublicationPublished {
		return nil, conflict("only ready draft or unpublished publications can be submitted for review", nil)
	}
	fb.ReviewState = models.ReviewPending
	fb.ReviewNote = strings.TrimSpace(note)
	fb.ReviewedBy = ""
	fb.ReviewedAt = nil
	if err := s.store.UpdateFlipbookMetadata(ctx, fb); err != nil {
		return nil, repositoryError(err)
	}
	result := publicFlipbook(fb)
	return &result, nil
}

func (s *Service) ApproveReview(ctx context.Context, actor Actor, workspaceID, flipbookID, note string) (*models.Flipbook, error) {
	return s.setReview(ctx, actor, workspaceID, flipbookID, note, models.ReviewApproved)
}

func (s *Service) RejectReview(ctx context.Context, actor Actor, workspaceID, flipbookID, note string) (*models.Flipbook, error) {
	return s.setReview(ctx, actor, workspaceID, flipbookID, note, models.ReviewRejected)
}

func (s *Service) setReview(ctx context.Context, actor Actor, workspaceID, flipbookID, note string, state models.ReviewState) (*models.Flipbook, error) {
	fb, _, err := s.reviewContext(ctx, actor, workspaceID, flipbookID, models.RoleAdmin)
	if err != nil {
		return nil, err
	}
	if fb.ReviewState != models.ReviewPending {
		return nil, newHTTPError(http.StatusConflict, CodeReviewState, "publication is not awaiting review", nil)
	}
	now := s.timeNow()
	fb.ReviewState = state
	fb.ReviewNote = strings.TrimSpace(note)
	fb.ReviewedBy = actor.UserID
	fb.ReviewedAt = &now
	if err := s.store.UpdateFlipbookMetadata(ctx, fb); err != nil {
		return nil, repositoryError(err)
	}
	result := publicFlipbook(fb)
	return &result, nil
}

func (s *Service) reviewContext(ctx context.Context, actor Actor, workspaceID, flipbookID string, role models.Role) (*models.Flipbook, *models.Workspace, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, nil, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, role); err != nil {
		return nil, nil, err
	}
	workspace, err := s.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, repositoryError(err)
	}
	return fb, workspace, nil
}

func (s *Service) Publish(ctx context.Context, actor Actor, workspaceID, flipbookID string, options PublishOptions) (*models.Flipbook, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RolePublisher); err != nil {
		return nil, err
	}
	workspace, err := s.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, repositoryError(err)
	}
	return s.publish(ctx, fb, workspace, options)
}

func (s *Service) publish(ctx context.Context, fb *models.Flipbook, workspace *models.Workspace, options PublishOptions) (*models.Flipbook, error) {
	now := s.timeNow()
	visibility := options.Visibility
	if visibility == "" {
		visibility = fb.Visibility
	}
	if visibility == "" {
		visibility = workspace.Settings.DefaultVisibility
	}
	publishAt := cloneTime(options.PublishAt)
	scheduled := publishAt != nil && publishAt.After(now)
	if publishAt != nil && !scheduled {
		publishAt = nil
	}
	candidate := *fb
	candidate.Visibility = visibility
	candidate.PublishAt = publishAt
	candidate.ExpiresAt = cloneTime(options.ExpiresAt)
	if err := validatePublication(&candidate, workspace, now); err != nil {
		return nil, err
	}
	if scheduled {
		if err := s.store.SetPublicationState(ctx, fb.WorkspaceID, fb.ID, models.PublicationDraft, visibility, now); err != nil {
			return nil, repositoryError(err)
		}
		candidate.PublicationState = models.PublicationDraft
		if err := s.store.UpdateFlipbookMetadata(ctx, &candidate); err != nil {
			return nil, repositoryError(err)
		}
		candidate.UnpublishedAt = &now
	} else {
		candidate.PublishAt = nil
		candidate.PublicationState = models.PublicationPublished
		if err := s.store.UpdateFlipbookMetadata(ctx, &candidate); err != nil {
			return nil, repositoryError(err)
		}
		if err := s.store.SetPublicationState(ctx, fb.WorkspaceID, fb.ID, models.PublicationPublished, visibility, now); err != nil {
			return nil, repositoryError(err)
		}
		candidate.PublishedAt = &now
		candidate.UnpublishedAt = nil
	}
	candidate.ApplyDefaults()
	result := publicFlipbook(&candidate)
	return &result, nil
}

func validatePublication(fb *models.Flipbook, workspace *models.Workspace, now time.Time) error {
	if fb.Status != models.StatusReady || fb.PageCount < 1 {
		return newHTTPError(http.StatusConflict, CodeNotReady, "publication must have valid converted pages before publishing", nil)
	}
	if fb.ArchivedAt != nil || fb.DeletedAt != nil {
		return conflict("archived or deleted publications cannot be published", nil)
	}
	switch fb.Visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted, models.VisibilityPrivate,
		models.VisibilityTeam, models.VisibilityPassword, models.VisibilityToken:
	default:
		return invalid("unsupported publication visibility")
	}
	if fb.Visibility == models.VisibilityPassword {
		if fb.PasswordHash == "" {
			return invalid("password visibility requires a password hash")
		}
		if _, err := bcrypt.Cost([]byte(fb.PasswordHash)); err != nil {
			return invalid("publication password hash is invalid")
		}
	}
	if workspace.Settings.RequirePublicationReview && fb.ReviewState != models.ReviewApproved {
		return newHTTPError(http.StatusConflict, CodeReviewRequired, "publication review approval is required", nil)
	}
	if fb.PublishAt != nil && fb.ExpiresAt != nil && !fb.ExpiresAt.After(*fb.PublishAt) {
		return invalid("publication expiry must be after its scheduled publish time")
	}
	if fb.ExpiresAt != nil && !fb.ExpiresAt.After(now) {
		return invalid("publication expiry must be in the future")
	}
	if fb.LeadGate.PreviewPages < 0 || fb.Sales.PreviewPages < 0 {
		return invalid("preview page counts must not be negative")
	}
	if fb.Sales.Enabled {
		if fb.Sales.PriceCents <= 0 || len(fb.Sales.Currency) != 3 {
			return invalid("paid publications require a positive price and three-letter currency")
		}
	}
	return nil
}

func (s *Service) Unpublish(ctx context.Context, actor Actor, workspaceID, flipbookID string) (*models.Flipbook, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RolePublisher); err != nil {
		return nil, err
	}
	return s.unpublish(ctx, fb)
}

func (s *Service) unpublish(ctx context.Context, fb *models.Flipbook) (*models.Flipbook, error) {
	now := s.timeNow()
	if err := s.store.SetPublicationState(ctx, fb.WorkspaceID, fb.ID, models.PublicationUnpublished, fb.Visibility, now); err != nil {
		return nil, repositoryError(err)
	}
	candidate := *fb
	candidate.PublicationState = models.PublicationUnpublished
	candidate.UnpublishedAt = &now
	candidate.PublishAt = nil
	candidate.ExpiresAt = nil
	candidate.IsPublic = false
	if err := s.store.UpdateFlipbookMetadata(ctx, &candidate); err != nil {
		return nil, repositoryError(err)
	}
	result := publicFlipbook(&candidate)
	return &result, nil
}

func (s *Service) ProcessScheduled(ctx context.Context, now time.Time) (*ScheduleReport, error) {
	if now.IsZero() {
		now = s.timeNow()
	}
	due, err := s.store.ListDuePublications(ctx, now.UTC())
	if err != nil {
		return nil, repositoryError(err)
	}
	report := &ScheduleReport{Failed: []LifecycleFailure{}}
	for _, item := range due {
		workspace, workspaceErr := s.store.GetWorkspace(ctx, item.WorkspaceID)
		if workspaceErr != nil {
			report.Failed = append(report.Failed, LifecycleFailure{WorkspaceID: item.WorkspaceID, FlipbookID: item.ID, Err: repositoryError(workspaceErr)})
			continue
		}
		switch item.PublicationState {
		case models.PublicationDraft:
			if item.PublishAt == nil || item.PublishAt.After(now) {
				continue
			}
			options := PublishOptions{Visibility: item.Visibility, ExpiresAt: item.ExpiresAt}
			if _, err := s.publish(ctx, item, workspace, options); err != nil {
				report.Failed = append(report.Failed, LifecycleFailure{WorkspaceID: item.WorkspaceID, FlipbookID: item.ID, Err: err})
			} else {
				report.Published++
			}
		case models.PublicationPublished:
			if item.ExpiresAt == nil || item.ExpiresAt.After(now) {
				continue
			}
			if _, err := s.unpublish(ctx, item); err != nil {
				report.Failed = append(report.Failed, LifecycleFailure{WorkspaceID: item.WorkspaceID, FlipbookID: item.ID, Err: err})
			} else {
				report.Unpublished++
			}
		}
	}
	return report, nil
}

func (s *Service) Archive(ctx context.Context, actor Actor, workspaceID, flipbookID, reason string) error {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RolePublisher); err != nil {
		return err
	}
	return repositoryError(s.store.ArchiveFlipbook(ctx, workspaceID, flipbookID, strings.TrimSpace(reason)))
}

func (s *Service) Restore(ctx context.Context, actor Actor, workspaceID, flipbookID string) (*models.Flipbook, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RolePublisher); err != nil {
		return nil, err
	}
	if err := s.store.RestoreFlipbook(ctx, workspaceID, flipbookID); err != nil {
		return nil, repositoryError(err)
	}
	restored, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	result := publicFlipbook(restored)
	return &result, nil
}

func (s *Service) SchedulePurge(ctx context.Context, actor Actor, workspaceID, flipbookID string) (time.Time, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return time.Time{}, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RoleAdmin); err != nil {
		return time.Time{}, err
	}
	if err := s.store.SoftDeleteFlipbook(ctx, workspaceID, flipbookID, s.purgeDelay); err != nil {
		return time.Time{}, repositoryError(err)
	}
	return s.timeNow().Add(s.purgeDelay), nil
}

func (s *Service) PurgeDue(ctx context.Context, now time.Time) (*PurgeReport, error) {
	if now.IsZero() {
		now = s.timeNow()
	}
	items, err := s.store.ListFlipbooksPendingPurge(ctx, now.UTC())
	if err != nil {
		return nil, repositoryError(err)
	}
	report := &PurgeReport{Failed: []LifecycleFailure{}}
	for _, fb := range items {
		if err := s.purgeOne(ctx, fb); err != nil {
			report.Failed = append(report.Failed, LifecycleFailure{WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Err: err})
			continue
		}
		report.Purged++
	}
	return report, nil
}

func (s *Service) purgeOne(ctx context.Context, fb *models.Flipbook) error {
	revisions, err := s.store.ListRevisions(ctx, fb.WorkspaceID, fb.ID)
	if err != nil {
		return repositoryError(err)
	}
	if err := s.assets.DeleteFlipbookContext(ctx, fb.ID); err != nil {
		return internal(err)
	}
	ids := make(map[string]struct{})
	if fb.GridFSFileID != "" {
		ids[fb.GridFSFileID] = struct{}{}
	}
	for _, revision := range revisions {
		if revision.OriginalGridFSFileID != "" {
			ids[revision.OriginalGridFSFileID] = struct{}{}
		}
	}
	for id := range ids {
		if err := s.store.DeleteFromGridFS(ctx, id); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return repositoryError(err)
		}
	}
	return repositoryError(s.store.PurgeFlipbook(ctx, fb.WorkspaceID, fb.ID))
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := value.UTC()
	return &clone
}
