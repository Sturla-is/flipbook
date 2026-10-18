package publishing

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

func TestRevisionActivationAndRollback(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, first := installTestPublication(store, assets, now)
	addMembership(store, fb.WorkspaceID, "editor", models.RoleEditor)
	second := &models.Revision{
		ID: "revision-2", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Number: 2, AssetVersion: 2, PageCount: 8, Status: models.RevisionReady, ValidatedAt: &now,
	}
	store.revisions[second.ID] = second
	service := testService(t, store, assets, now)

	activated, err := service.ActivateRevision(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, second.ID)
	if err != nil || activated.Flipbook.CurrentRevisionID != second.ID || activated.Flipbook.AssetVersion != 2 || activated.Flipbook.PageCount != 8 {
		t.Fatalf("revision activation = %+v, %v", activated, err)
	}
	if assets.active[fb.ID] != 2 || store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)].CurrentRevisionID != second.ID {
		t.Fatalf("activation pointers diverged: assets=%v db=%+v", assets.active, store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)])
	}
	rolledBack, err := service.RollbackRevision(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, first.ID)
	if err != nil || !rolledBack.Rollback || rolledBack.Flipbook.CurrentRevisionID != first.ID || assets.active[fb.ID] != 1 {
		t.Fatalf("revision rollback = %+v, assets=%v, %v", rolledBack, assets.active, err)
	}
	if _, err := service.RollbackRevision(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, second.ID); ErrorCodeOf(err) != CodeConflict {
		t.Fatalf("newer rollback target error = %v", err)
	}
	second.ValidatedAt = nil
	if _, err := service.ActivateRevision(ctx, Actor{UserID: "editor"}, fb.WorkspaceID, fb.ID, second.ID); ErrorCodeOf(err) != CodeConflict {
		t.Fatalf("unvalidated revision error = %v", err)
	}
}

func TestRevisionActivationCompensatesDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, first := installTestPublication(store, assets, now)
	second := &models.Revision{
		ID: "revision-2", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Number: 2, AssetVersion: 2, PageCount: 8, Status: models.RevisionReady, ValidatedAt: &now,
	}
	store.revisions[second.ID] = second
	store.activateFailures = 1
	store.activateErr = errors.New("database pointer failed")
	service := testService(t, store, assets, now)

	if _, err := service.ActivateRevision(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, second.ID); HTTPStatus(err) != 500 {
		t.Fatalf("activation failure status = %d (%v)", HTTPStatus(err), err)
	}
	if assets.active[fb.ID] != first.Number {
		t.Fatalf("storage pointer was not restored: %d", assets.active[fb.ID])
	}
	if got := store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)].CurrentRevisionID; got != first.ID {
		t.Fatalf("database pointer was not restored: %q", got)
	}
	if !reflect.DeepEqual(assets.activationCalls, []string{"book:2", "book:1"}) {
		t.Fatalf("storage compensation calls = %#v", assets.activationCalls)
	}
	if !reflect.DeepEqual(store.activationCalls, []string{second.ID, first.ID}) {
		t.Fatalf("database compensation calls = %#v", store.activationCalls)
	}
}

func TestRevisionStorageFailureLeavesDatabaseLive(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, first := installTestPublication(store, assets, now)
	second := &models.Revision{
		ID: "revision-2", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Number: 2, AssetVersion: 2, PageCount: 8, Status: models.RevisionReady, ValidatedAt: &now,
	}
	store.revisions[second.ID] = second
	assets.activateErr = errors.New("tampered artifacts")
	service := testService(t, store, assets, now)
	if _, err := service.ActivateRevision(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, second.ID); err == nil {
		t.Fatal("invalid storage revision was activated")
	}
	if len(store.activationCalls) != 0 || store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)].CurrentRevisionID != first.ID {
		t.Fatalf("database changed after storage validation failure: %#v", store.activationCalls)
	}
}

func TestPurgeDueDeletesAllStoresAndRetriesFailures(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, first := installTestPublication(store, assets, now)
	fb.GridFSFileID = "grid-main"
	first.OriginalGridFSFileID = "grid-main"
	second := &models.Revision{
		ID: "revision-2", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Number: 2, AssetVersion: 2, PageCount: 8, Status: models.RevisionSuperseded,
		OriginalGridFSFileID: "grid-second", ValidatedAt: &now,
	}
	store.revisions[second.ID] = second
	deleted, purgeAfter := now.Add(-24*time.Hour), now.Add(-time.Hour)
	fb.DeletedAt, fb.PurgeAfter = &deleted, &purgeAfter
	service := testService(t, store, assets, now)

	store.gridErr = errors.New("GridFS unavailable")
	report, err := service.PurgeDue(ctx, now)
	if err != nil || report.Purged != 0 || len(report.Failed) != 1 {
		t.Fatalf("failed purge report = %+v, %v", report, err)
	}
	if store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)] == nil {
		t.Fatal("database record was purged despite storage failure")
	}
	store.gridErr = nil
	report, err = service.PurgeDue(ctx, now)
	if err != nil || report.Purged != 1 || len(report.Failed) != 0 {
		t.Fatalf("purge retry report = %+v, %v", report, err)
	}
	if !reflect.DeepEqual(assets.deleteCalls, []string{fb.ID, fb.ID}) {
		t.Fatalf("idempotent storage retries = %#v", assets.deleteCalls)
	}
	sort.Strings(store.deletedGridIDs)
	if !reflect.DeepEqual(store.deletedGridIDs, []string{"grid-main", "grid-second"}) {
		t.Fatalf("GridFS deletion IDs = %#v", store.deletedGridIDs)
	}
	if store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)] != nil || len(store.revisions) != 0 {
		t.Fatalf("database records survived purge: %+v, %+v", store.flipbooks, store.revisions)
	}
}
