package worker

import (
	"context"
	"log"

	"github.com/metavert-io/flipbook/internal/converter"
	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/storage"
)

type Job struct {
	FlipbookID string
	SourcePath string
}

type Worker struct {
	jobs    chan Job
	db      *database.DB
	storage *storage.Storage
	conv    *converter.Converter
}

func New(db *database.DB, store *storage.Storage, conv *converter.Converter) *Worker {
	return &Worker{
		jobs:    make(chan Job, 100),
		db:      db,
		storage: store,
		conv:    conv,
	}
}

func (w *Worker) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case job := <-w.jobs:
				w.processJob(ctx, job)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (w *Worker) Enqueue(job Job) {
	w.jobs <- job
}

func (w *Worker) processJob(ctx context.Context, job Job) {
	log.Printf("[worker] Starting conversion for %s", job.FlipbookID)

	if err := w.db.UpdateStatus(job.FlipbookID, models.StatusConverting, ""); err != nil {
		log.Printf("[worker] Failed to update status: %v", err)
		return
	}

	if err := w.storage.EnsureDirs(job.FlipbookID); err != nil {
		log.Printf("[worker] Failed to create dirs: %v", err)
		w.db.UpdateStatus(job.FlipbookID, models.StatusError, err.Error())
		return
	}

	result, err := w.conv.Convert(
		ctx,
		job.SourcePath,
		w.storage.PagesDir(job.FlipbookID),
		w.storage.ThumbsDir(job.FlipbookID),
	)
	if err != nil {
		log.Printf("[worker] Conversion failed for %s: %v", job.FlipbookID, err)
		w.db.UpdateStatus(job.FlipbookID, models.StatusError, err.Error())
		return
	}

	if err := w.db.UpdateConversionResult(job.FlipbookID, result.PageCount, result.Width, result.Height); err != nil {
		log.Printf("[worker] Failed to save result: %v", err)
		w.db.UpdateStatus(job.FlipbookID, models.StatusError, err.Error())
		return
	}

	log.Printf("[worker] Conversion complete for %s: %d pages", job.FlipbookID, result.PageCount)
}
