package publishing

import (
	"context"
	"errors"
	"fmt"

	"github.com/metavert-io/flipbook/internal/models"
)

func (s *Service) ActivateRevision(ctx context.Context, actor Actor, workspaceID, flipbookID, revisionID string) (*ActivationResult, error) {
	return s.activateRevision(ctx, actor, workspaceID, flipbookID, revisionID, false)
}

func (s *Service) RollbackRevision(ctx context.Context, actor Actor, workspaceID, flipbookID, revisionID string) (*ActivationResult, error) {
	return s.activateRevision(ctx, actor, workspaceID, flipbookID, revisionID, true)
}

func (s *Service) activateRevision(ctx context.Context, actor Actor, workspaceID, flipbookID, revisionID string, rollback bool) (*ActivationResult, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RoleEditor); err != nil {
		return nil, err
	}
	current, err := s.store.GetCurrentRevision(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	target, err := s.store.GetRevision(ctx, workspaceID, revisionID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if target.FlipbookID != fb.ID || target.Number < 1 || target.AssetVersion < 1 || target.PageCount < 1 || target.ValidatedAt == nil {
		return nil, conflict("revision is not a validated publication artifact", nil)
	}
	switch target.Status {
	case models.RevisionReady, models.RevisionActive, models.RevisionSuperseded:
	default:
		return nil, conflict("revision is not ready for activation", nil)
	}
	if rollback && target.Number >= current.Number {
		return nil, conflict("rollback target must be older than the active revision", nil)
	}
	if target.ID == current.ID {
		return &ActivationResult{Flipbook: publicFlipbook(fb), Revision: *publicRevision(target), Rollback: rollback}, nil
	}
	if err := s.assets.ActivateRevision(fb.ID, target.Number); err != nil {
		return nil, internal(fmt.Errorf("validate and activate revision storage: %w", err))
	}
	if err := s.store.ActivateRevision(ctx, workspaceID, flipbookID, revisionID); err != nil {
		compensation := []error{err}
		if storageErr := s.assets.ActivateRevision(fb.ID, current.Number); storageErr != nil {
			compensation = append(compensation, fmt.Errorf("restore previous storage revision: %w", storageErr))
		}
		if databaseErr := s.store.ActivateRevision(ctx, workspaceID, flipbookID, current.ID); databaseErr != nil {
			compensation = append(compensation, fmt.Errorf("restore previous database revision: %w", databaseErr))
		}
		return nil, internal(errors.Join(compensation...))
	}
	now := s.timeNow()
	updated := *fb
	updated.CurrentRevisionID = target.ID
	updated.AssetVersion = target.AssetVersion
	updated.PageCount = target.PageCount
	updated.Status = models.StatusReady
	updated.ErrorMessage = ""
	updated.ConvertedAt = &now
	updated.UpdatedAt = now
	activated := *target
	activated.Status = models.RevisionActive
	activated.ActivatedAt = &now
	activated.UpdatedAt = now
	return &ActivationResult{Flipbook: publicFlipbook(&updated), Revision: activated, Rollback: rollback}, nil
}
