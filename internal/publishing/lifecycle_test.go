package publishing

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
)

func addMembership(store *fakeStore, workspaceID, userID string, role models.Role) {
	store.memberships[membershipKey(workspaceID, userID)] = &models.Membership{WorkspaceID: workspaceID, UserID: userID, Role: role}
}

func TestReviewApprovalAndPublishRoles(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	fb.PublicationState = models.PublicationDraft
	fb.IsPublic = false
	store.workspaces[fb.WorkspaceID].Settings.RequirePublicationReview = true
	addMembership(store, fb.WorkspaceID, "editor", models.RoleEditor)
	addMembership(store, fb.WorkspaceID, "publisher", models.RolePublisher)
	addMembership(store, fb.WorkspaceID, "admin", models.RoleAdmin)
	service := testService(t, store, assets, now)

	if _, err := service.Publish(ctx, Actor{UserID: "publisher"}, fb.WorkspaceID, fb.ID, PublishOptions{}); ErrorCodeOf(err) != CodeReviewRequired {
		t.Fatalf("publish without review error = %v", err)
	}
	pending, err := service.SubmitForReview(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, "please review")
	if err != nil || pending.ReviewState != models.ReviewPending {
		t.Fatalf("submit review = %+v, %v", pending, err)
	}
	if _, err := service.ApproveReview(ctx, Actor{UserID: "publisher"}, fb.WorkspaceID, fb.ID, ""); HTTPStatus(err) != http.StatusForbidden {
		t.Fatalf("publisher could approve review: %v", err)
	}
	approved, err := service.ApproveReview(ctx, Actor{UserID: "admin"}, fb.WorkspaceID, fb.ID, "approved")
	if err != nil || approved.ReviewState != models.ReviewApproved || approved.ReviewedBy != "admin" || approved.ReviewedAt == nil {
		t.Fatalf("approve review = %+v, %v", approved, err)
	}
	published, err := service.Publish(ctx, Actor{UserID: "publisher"}, fb.WorkspaceID, fb.ID, PublishOptions{Visibility: models.VisibilityUnlisted})
	if err != nil || published.PublicationState != models.PublicationPublished || published.Visibility != models.VisibilityUnlisted || !published.IsPublic {
		t.Fatalf("reviewed publish = %+v, %v", published, err)
	}
	if !store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)].IsPublic {
		t.Fatal("legacy is_public was not synchronized for unlisted publication")
	}
}

func TestRejectedReviewCannotPublish(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	fb.PublicationState = models.PublicationDraft
	store.workspaces[fb.WorkspaceID].Settings.RequirePublicationReview = true
	addMembership(store, fb.WorkspaceID, "editor", models.RoleEditor)
	service := testService(t, store, assets, now)
	if _, err := service.SubmitForReview(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RejectReview(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, "changes needed"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, PublishOptions{}); ErrorCodeOf(err) != CodeReviewRequired {
		t.Fatalf("rejected publication publish error = %v", err)
	}
}

func TestScheduledPublishAndExpiryProcessing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	current := now
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	fb.PublicationState = models.PublicationDraft
	fb.IsPublic = false
	service := testService(t, store, assets, now)
	service.now = func() time.Time { return current }
	publishAt, expiresAt := now.Add(time.Hour), now.Add(2*time.Hour)

	scheduled, err := service.Publish(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, PublishOptions{
		Visibility: models.VisibilityPublic, PublishAt: &publishAt, ExpiresAt: &expiresAt,
	})
	if err != nil || scheduled.PublicationState != models.PublicationDraft || scheduled.IsPublic {
		t.Fatalf("scheduled publish = %+v, %v", scheduled, err)
	}
	stored := store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)]
	if stored.PublishAt == nil || !stored.PublishAt.Equal(publishAt) || stored.IsPublic {
		t.Fatalf("schedule was not persisted safely: %+v", stored)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("future publication was visible: %v", err)
	}
	current = publishAt
	report, err := service.ProcessScheduled(ctx, current)
	if err != nil || report.Published != 1 || len(report.Failed) != 0 {
		t.Fatalf("due publish report = %+v, %v", report, err)
	}
	stored = store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)]
	if stored.PublicationState != models.PublicationPublished || !stored.IsPublic || stored.PublishAt != nil {
		t.Fatalf("due publication did not activate: %+v", stored)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{}); err != nil {
		t.Fatal(err)
	}
	current = expiresAt
	report, err = service.ProcessScheduled(ctx, current)
	if err != nil || report.Unpublished != 1 || len(report.Failed) != 0 {
		t.Fatalf("due expiry report = %+v, %v", report, err)
	}
	stored = store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)]
	if stored.PublicationState != models.PublicationUnpublished || stored.IsPublic || stored.ExpiresAt != nil {
		t.Fatalf("expired publication did not unpublish: %+v", stored)
	}
}

func TestScheduledProcessingContinuesAfterFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, revision := installTestPublication(store, assets, now)
	past := now.Add(-time.Minute)
	fb.PublicationState, fb.PublishAt = models.PublicationDraft, &past
	second := cloneFlipbook(fb)
	second.ID, second.Slug, second.CurrentRevisionID = "broken", "broken", "broken-revision"
	second.Status = models.StatusError
	store.flipbooks[publicationKey(second.WorkspaceID, second.ID)] = second
	secondRevision := cloneRevision(revision)
	secondRevision.ID, secondRevision.FlipbookID = "broken-revision", second.ID
	store.revisions[secondRevision.ID] = secondRevision
	service := testService(t, store, assets, now)
	service.now = func() time.Time { return now }

	report, err := service.ProcessScheduled(ctx, now)
	if err != nil || report.Published != 1 || len(report.Failed) != 1 || report.Failed[0].FlipbookID != second.ID {
		t.Fatalf("partial schedule report = %+v, %v", report, err)
	}
}

func TestPublishValidation(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*models.Flipbook, *PublishOptions)
		code   ErrorCode
	}{
		{name: "not ready", change: func(fb *models.Flipbook, _ *PublishOptions) { fb.Status = models.StatusPending }, code: CodeNotReady},
		{name: "password missing", change: func(_ *models.Flipbook, options *PublishOptions) { options.Visibility = models.VisibilityPassword }, code: CodeInvalid},
		{name: "invalid sales", change: func(fb *models.Flipbook, _ *PublishOptions) { fb.Sales.Enabled = true }, code: CodeInvalid},
		{name: "past expiry", change: func(_ *models.Flipbook, options *PublishOptions) {
			past := now.Add(-time.Hour)
			options.ExpiresAt = &past
		}, code: CodeInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, assets := newFakeStore(), newFakeAssets()
			fb, _ := installTestPublication(store, assets, now)
			service := testService(t, store, assets, now)
			options := PublishOptions{}
			test.change(fb, &options)
			if _, err := service.Publish(context.Background(), Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, options); ErrorCodeOf(err) != test.code {
				t.Fatalf("publish validation error = %v, want %q", err, test.code)
			}
		})
	}
}

func TestArchiveRestoreAndPurgeScheduling(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	addMembership(store, fb.WorkspaceID, "publisher", models.RolePublisher)
	service := testService(t, store, assets, now)

	if err := service.Archive(ctx, Actor{UserID: "publisher"}, fb.WorkspaceID, fb.ID, "retired"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Reader: Reader{UserID: "owner"}}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("archived publication remains visible: %v", err)
	}
	restored, err := service.Restore(ctx, Actor{UserID: "publisher"}, fb.WorkspaceID, fb.ID)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if _, err := service.SchedulePurge(ctx, Actor{UserID: "publisher"}, fb.WorkspaceID, fb.ID); HTTPStatus(err) != http.StatusForbidden {
		t.Fatalf("publisher scheduled permanent deletion: %v", err)
	}
	purgeAfter, err := service.SchedulePurge(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID)
	if err != nil || purgeAfter.IsZero() {
		t.Fatalf("purge scheduling = %v, %v", purgeAfter, err)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Reader: Reader{UserID: "owner"}}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("soft-deleted publication remains visible: %v", err)
	}
}

func TestEditableSlugAndPermanentRedirect(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	addMembership(store, fb.WorkspaceID, "editor", models.RoleEditor)
	addMembership(store, fb.WorkspaceID, "reader", models.RoleReader)
	service := testService(t, store, assets, now)

	if _, err := service.ChangeSlug(ctx, Actor{UserID: "reader"}, fb.WorkspaceID, fb.ID, "forbidden"); HTTPStatus(err) != http.StatusForbidden {
		t.Fatalf("reader changed slug: %v", err)
	}
	slug, err := service.ChangeSlug(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, " Annual Report 2026 ")
	if err != nil || slug != "annual-report-2026" {
		t.Fatalf("change slug = %q, %v", slug, err)
	}
	_, redirected, err := service.ResolveViewerSlug(ctx, fb.WorkspaceID, "report", AccessRequest{})
	if err != nil || !redirected {
		t.Fatalf("old slug redirect = %v, %v", redirected, err)
	}
	auth, redirected, err := service.ResolveViewerSlug(ctx, fb.WorkspaceID, "annual-report-2026", AccessRequest{})
	if err != nil || redirected || auth.Flipbook.Slug != slug {
		t.Fatalf("current slug resolution = %+v, %v, %v", auth, redirected, err)
	}
	if _, err := service.ChangeSlug(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, "---"); ErrorCodeOf(err) != CodeInvalid {
		t.Fatalf("empty slug error = %v", err)
	}
}
