package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/storage"
)

func TestLegacyEnqueueUsesDurableIdempotentJobs(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	sourcePath := store.OriginalPath("book", ".pdf")
	writeSource(t, sourcePath)
	db.flipbooks["book"] = &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", Filename: "book.pdf", Status: models.StatusPending,
		CurrentRevisionID: "revision-1", PublicationState: models.PublicationDraft,
	}
	db.revisions["revision-1"] = &models.Revision{
		ID: "revision-1", WorkspaceID: "workspace", FlipbookID: "book", Number: 1, Status: models.RevisionPending, SourceFilename: "book.pdf",
	}
	worker := testWorker(t, db, store, &fakeConverter{})

	first, err := worker.EnqueueContext(context.Background(), Job{FlipbookID: "book", SourcePath: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	second, err := worker.EnqueueContext(context.Background(), Job{FlipbookID: "book", SourcePath: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(db.jobs) != 2 {
		t.Fatalf("idempotent enqueue created unexpected jobs: first=%s second=%s jobs=%d", first.ID, second.ID, len(db.jobs))
	}
	if first.Type != models.JobConvert || first.Status != models.JobQueued || first.Payload["source_checksum"] == "" {
		t.Fatalf("durable conversion job incorrect: %+v", first)
	}
	backupFound := false
	for _, job := range db.jobs {
		backupFound = backupFound || job.Type == models.JobBackup
	}
	if !backupFound {
		t.Fatal("initial upload did not enqueue durable GridFS backup")
	}

	db.jobs[first.ID].Status = models.JobDead
	if err := worker.Enqueue(Job{FlipbookID: "book", SourcePath: sourcePath}); err != nil {
		t.Fatal(err)
	}
	if db.jobs[first.ID].Status != models.JobQueued || db.jobs[first.ID].Attempts != 0 {
		t.Fatalf("terminal job was not retried: %+v", db.jobs[first.ID])
	}
}

func TestScheduledPublicationLifecycle(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	now := time.Unix(1000, 0).UTC()
	past := now.Add(-time.Minute)
	db.flipbooks["publish"] = &models.Flipbook{
		ID: "publish", WorkspaceID: "workspace", Status: models.StatusReady,
		PublicationState: models.PublicationDraft, Visibility: models.VisibilityPublic,
		ReviewState: models.ReviewNotRequired, PublishAt: &past,
	}
	db.flipbooks["expire"] = &models.Flipbook{
		ID: "expire", WorkspaceID: "workspace", Status: models.StatusReady,
		PublicationState: models.PublicationPublished, Visibility: models.VisibilityPublic,
		ReviewState: models.ReviewNotRequired, ExpiresAt: &past,
	}
	db.flipbooks["not-ready"] = &models.Flipbook{
		ID: "not-ready", WorkspaceID: "workspace", Status: models.StatusPending,
		PublicationState: models.PublicationDraft, ReviewState: models.ReviewNotRequired, PublishAt: &past,
	}
	worker := testWorker(t, db, store, &fakeConverter{})
	count, err := worker.SchedulePublications(context.Background())
	if err != nil || count != 2 || len(db.jobs) != 2 {
		t.Fatalf("scheduled jobs: count=%d jobs=%d err=%v", count, len(db.jobs), err)
	}
	for _, job := range db.jobs {
		runningJob(worker, job)
		if err := worker.processLeased(context.Background(), job); err != nil {
			t.Fatalf("process %s: %v", job.Type, err)
		}
	}
	if db.flipbooks["publish"].PublicationState != models.PublicationPublished || !db.flipbooks["publish"].IsPublic {
		t.Fatalf("scheduled publish failed: %+v", db.flipbooks["publish"])
	}
	if db.flipbooks["expire"].PublicationState != models.PublicationUnpublished || db.flipbooks["expire"].IsPublic {
		t.Fatalf("scheduled expiry failed: %+v", db.flipbooks["expire"])
	}
}

func TestChangedScheduleIsNotOverridden(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	now := time.Unix(1000, 0).UTC()
	future := now.Add(time.Hour)
	db.flipbooks["book"] = &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", Status: models.StatusReady,
		PublicationState: models.PublicationDraft, Visibility: models.VisibilityPublic,
		ReviewState: models.ReviewNotRequired, PublishAt: &future,
	}
	worker := testWorker(t, db, store, &fakeConverter{})
	job := runningJob(worker, &models.Job{
		ID: "stale-schedule", WorkspaceID: "workspace", FlipbookID: "book", Type: models.JobPublish,
		Payload: map[string]any{"scheduled": true, "scheduled_for": now.Add(-time.Hour).Format(time.RFC3339Nano)},
	})
	db.jobs[job.ID] = job
	if err := worker.processLeased(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if db.flipbooks["book"].PublicationState != models.PublicationDraft {
		t.Fatal("stale scheduled job published a rescheduled flipbook")
	}
}

func TestBackupVerificationAndRetry(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	sourcePath := filepath.Join(t.TempDir(), "original.pdf")
	writeSource(t, sourcePath)
	source := inspectSource(sourcePath)
	db.flipbooks["book"] = &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", Filename: "book.pdf", CurrentRevisionID: "revision-1", GridFSFileID: "grid-existing",
	}
	db.gridfs["grid-existing"] = []byte("%PDF-1.4\nworker fixture\n")
	worker := testWorker(t, db, store, &fakeConverter{})
	newBackupJob := func(id string) *models.Job {
		job := runningJob(worker, &models.Job{
			ID: id, WorkspaceID: "workspace", FlipbookID: "book", RevisionID: "revision-1", Type: models.JobBackup,
			Payload: map[string]any{
				"source_path": sourcePath, "source_checksum": source.SHA256, "source_size": source.Size, "source_filename": "book.pdf",
			},
		})
		db.jobs[id] = job
		return job
	}

	job := newBackupJob("backup-verify")
	if err := worker.processLeased(context.Background(), job); err != nil {
		t.Fatalf("verify existing backup: %v", err)
	}
	if db.uploadCount != 0 || db.flipbooks["book"].BackupStatus != "complete" {
		t.Fatalf("valid backup was re-uploaded or not marked complete: uploads=%d backup=%s", db.uploadCount, db.flipbooks["book"].BackupStatus)
	}

	db.gridfs["grid-existing"] = []byte("corrupt")
	job = newBackupJob("backup-replace")
	if err := worker.processLeased(context.Background(), job); err != nil {
		t.Fatalf("replace corrupt backup: %v", err)
	}
	if db.uploadCount != 1 || db.flipbooks["book"].GridFSFileID == "grid-existing" {
		t.Fatalf("corrupt backup was not replaced: uploads=%d id=%s", db.uploadCount, db.flipbooks["book"].GridFSFileID)
	}

	db.uploadErr = errors.New("GridFS unavailable")
	db.flipbooks["book"].GridFSFileID = ""
	job = newBackupJob("backup-retry")
	if err := worker.processLeased(context.Background(), job); err == nil {
		t.Fatal("expected backup failure")
	}
	if db.jobs[job.ID].Status != models.JobRetry || db.jobs[job.ID].LastError == "" {
		t.Fatalf("backup failure is not retryable/observable: %+v", db.jobs[job.ID])
	}
}

func TestBackupRetryReusesUploadedGridFSObject(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	sourcePath := filepath.Join(t.TempDir(), "original.pdf")
	writeSource(t, sourcePath)
	source := inspectSource(sourcePath)
	db.flipbooks["book"] = &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", Filename: "book.pdf", CurrentRevisionID: "revision-1",
	}
	worker := testWorker(t, db, store, &fakeConverter{})
	job := runningJob(worker, &models.Job{
		ID: "backup-resume", WorkspaceID: "workspace", FlipbookID: "book", RevisionID: "revision-1", Type: models.JobBackup,
		Payload: map[string]any{
			"source_path": sourcePath, "source_checksum": source.SHA256, "source_size": source.Size, "source_filename": "book.pdf",
		},
	})
	db.jobs[job.ID] = job
	db.setGridFSErr = errors.New("database temporarily unavailable")
	progress := &progressState{}
	if err := worker.processBackup(context.Background(), job, progress); err == nil {
		t.Fatal("expected backup metadata failure")
	}
	if db.uploadCount != 1 || job.ProgressMessage != "gridfs:grid-1" {
		t.Fatalf("upload checkpoint missing: uploads=%d progress=%q", db.uploadCount, job.ProgressMessage)
	}

	db.setGridFSErr = nil
	if err := worker.processBackup(context.Background(), job, progress); err != nil {
		t.Fatalf("resume backup: %v", err)
	}
	if db.uploadCount != 1 || db.flipbooks["book"].GridFSFileID != "grid-1" {
		t.Fatalf("retry duplicated GridFS upload: uploads=%d id=%s", db.uploadCount, db.flipbooks["book"].GridFSFileID)
	}
}

func TestExpiredLeaseIsRecovered(t *testing.T) {
	db := newFakeDB()
	store := storage.New(filepath.Join(t.TempDir(), "flipbooks"))
	db.flipbooks["book"] = &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", PublicationState: models.PublicationUnpublished, Visibility: models.VisibilityPrivate,
	}
	worker := testWorker(t, db, store, &fakeConverter{})
	expired := time.Now().Add(-time.Minute)
	job := &models.Job{
		ID: "expired", WorkspaceID: "workspace", FlipbookID: "book", Type: models.JobUnpublish,
		Status: models.JobRunning, LeaseOwner: "crashed-worker", LeaseExpiresAt: &expired,
		Attempts: 1, MaxAttempts: 3,
	}
	db.jobs[job.ID] = job
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("recover expired lease: processed=%v err=%v", processed, err)
	}
	if db.jobs[job.ID].Attempts != 2 || db.jobs[job.ID].Status != models.JobSucceeded {
		t.Fatalf("expired lease was not recovered: %+v", db.jobs[job.ID])
	}
}
