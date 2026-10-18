package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jonradoff/flipbook/internal/models"
)

func (w *Worker) processBackup(ctx context.Context, job *models.Job, progress *progressState) error {
	flipbook, err := w.db.GetFlipbookInWorkspace(ctx, job.WorkspaceID, job.FlipbookID)
	if err != nil {
		return err
	}
	if job.RevisionID != "" && flipbook.CurrentRevisionID != job.RevisionID {
		progress.Set(100, "backup superseded by the active revision")
		return nil
	}
	progress.Set(5, "checking original")
	source := inspectSource(payloadString(job.Payload, "source_path"))
	if source.SHA256 == "" {
		return errors.New("backup source is unavailable")
	}
	expectedChecksum := payloadString(job.Payload, "source_checksum")
	expectedSize := payloadInt64(job.Payload, "source_size")
	if !sourceMatches(source, expectedChecksum, expectedSize) {
		return errors.New("backup source differs from the queued revision")
	}
	filename := payloadString(job.Payload, "source_filename")
	if filename == "" {
		filename = flipbook.Filename
	}

	resumedFileID := ""
	if strings.HasPrefix(job.ProgressMessage, "gridfs:") {
		resumedFileID = strings.TrimPrefix(job.ProgressMessage, "gridfs:")
	}
	candidates := []string{resumedFileID, flipbook.GridFSFileID}
	verified := make(map[string]bool, len(candidates))
	for _, fileID := range candidates {
		if fileID == "" || verified[fileID] {
			continue
		}
		verified[fileID] = true
		progress.Set(35, "verifying existing GridFS backup")
		if err := w.verifyGridFS(ctx, fileID, source); err == nil {
			if err := w.db.SetGridFSFileID(flipbook.ID, fileID); err != nil {
				return err
			}
			progress.Set(100, "GridFS backup verified")
			return nil
		}
	}

	progress.Set(45, "uploading original to GridFS")
	file, err := os.Open(source.Path)
	if err != nil {
		return err
	}
	fileID, uploadErr := w.db.UploadToGridFS(ctx, filename, file)
	closeErr := file.Close()
	if uploadErr != nil {
		return uploadErr
	}
	if closeErr != nil {
		return closeErr
	}
	progress.Set(80, "gridfs:"+fileID)
	if err := w.db.HeartbeatJob(ctx, job.ID, w.opts.WorkerID, w.opts.LeaseDuration, 80, "gridfs:"+fileID); err != nil {
		return err
	}
	if err := w.verifyGridFS(ctx, fileID, source); err != nil {
		return fmt.Errorf("verify GridFS upload: %w", err)
	}
	if err := w.db.SetGridFSFileID(flipbook.ID, fileID); err != nil {
		return err
	}
	progress.Set(100, "GridFS backup complete")
	return nil
}

func (w *Worker) verifyGridFS(ctx context.Context, fileID string, expected sourceInfo) error {
	hash := sha256.New()
	written, err := w.db.DownloadFromGridFS(ctx, fileID, hash)
	if err != nil {
		return err
	}
	if written != expected.Size {
		return fmt.Errorf("GridFS size mismatch: got %d, want %d", written, expected.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected.SHA256) {
		return errors.New("GridFS checksum mismatch")
	}
	return nil
}

func hashReader(reader io.Reader) (int64, string, error) {
	hash := sha256.New()
	written, err := io.Copy(hash, reader)
	return written, hex.EncodeToString(hash.Sum(nil)), err
}
