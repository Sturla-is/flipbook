package publishing

import (
	"context"
	"strings"

	"github.com/jonradoff/flipbook/internal/models"
)

func (s *Service) ChangeSlug(ctx context.Context, actor Actor, workspaceID, flipbookID, slug string) (string, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return "", repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RoleEditor); err != nil {
		return "", err
	}
	slug = models.NormalizeSlug(strings.TrimSpace(slug))
	if slug == "" || len(slug) > 200 {
		return "", invalid("slug must be non-empty and at most 200 bytes")
	}
	if err := s.store.ChangeFlipbookSlug(ctx, workspaceID, flipbookID, slug); err != nil {
		return "", repositoryError(err)
	}
	return slug, nil
}
