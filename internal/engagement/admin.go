package engagement

import (
	"context"

	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
)

func (s *Service) Leads(ctx context.Context, filter database.LeadFilter) ([]*models.Lead, error) {
	return s.store.ListLeads(ctx, filter)
}

func (s *Service) LeadCount(ctx context.Context, workspaceID, flipbookID string) (int64, error) {
	return s.store.CountLeads(ctx, workspaceID, flipbookID)
}

func (s *Service) Flipbooks(ctx context.Context, workspaceID string) ([]*models.Flipbook, error) {
	flipbooks, _, err := s.store.SearchFlipbooks(ctx, database.FlipbookFilter{
		WorkspaceID: workspaceID, Limit: 500, Sort: "title",
	})
	return flipbooks, err
}

func (s *Service) Workspace(ctx context.Context, workspaceID string) (*models.Workspace, error) {
	workspace, err := s.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	return workspace, nil
}

type WebhookStatus struct {
	Enabled             bool
	MaxAttempts         int
	RetryBackoffSeconds int
	TimeoutSeconds      int
}

func (s *Service) WebhookStatus() WebhookStatus {
	return WebhookStatus{
		Enabled: s.webhooks.Enabled, MaxAttempts: s.webhooks.MaxAttempts,
		RetryBackoffSeconds: s.webhooks.RetryBackoffSeconds, TimeoutSeconds: s.webhooks.TimeoutSeconds,
	}
}
