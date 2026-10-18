package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

func NewRunner(api API, baseURL string, options Options) (*Runner, error) {
	if !options.DryRun && api == nil {
		return nil, errors.New("migration API client is required")
	}
	if options.ReportPath == "" {
		options.ReportPath = "issuu-migration-report.json"
	}
	if options.StatePath == "" {
		options.StatePath = options.ReportPath + ".state.json"
	}
	if options.StatePath == "-" {
		return nil, errors.New("state path must be a writable file")
	}
	if options.Concurrency == 0 {
		options.Concurrency = 2
	}
	if options.Concurrency < 1 || options.Concurrency > 16 {
		return nil, errors.New("migration concurrency must be between 1 and 16")
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = 3
	}
	if options.MaxAttempts < 1 || options.MaxAttempts > 10 {
		return nil, errors.New("migration attempts must be between 1 and 10")
	}
	if options.WaitTimeout == 0 {
		options.WaitTimeout = 15 * time.Minute
	}
	if options.WaitTimeout < 0 {
		return nil, errors.New("wait timeout must not be negative")
	}
	if options.PollInterval == 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.PollInterval < 10*time.Millisecond {
		return nil, errors.New("poll interval must be at least 10ms")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{api: api, options: options, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

func (r *Runner) Run(ctx context.Context) (*Report, error) {
	plan, err := BuildPlan(ctx, r.options)
	if err != nil {
		return nil, err
	}
	report := &Report{
		SchemaVersion: 1, RunID: uuid.NewString(), StartedAt: r.options.Now().UTC(),
		DryRun: r.options.DryRun, Items: make([]ReportItem, len(plan)),
	}
	previous := make(map[string]ReportItem)
	if r.options.Resume {
		loaded, err := loadReport(r.options.StatePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load migration state: %w", err)
		}
		if loaded != nil {
			for _, item := range loaded.Items {
				previous[item.ExternalID] = item
			}
		}
	}
	for index, item := range plan {
		entry := ReportItem{
			ExternalID: item.ExternalID, SourcePath: item.SourcePath, Title: item.Title,
			DesiredSlug: item.DesiredSlug, LegacySlug: item.LegacySlug, LegacyURL: item.LegacyURL,
			SHA256: item.SHA256, Status: StatusPlanned,
		}
		if old, ok := previous[item.ExternalID]; ok {
			entry.FlipbookID = old.FlipbookID
			entry.ViewerURL = old.ViewerURL
			entry.LegacyRedirectURL = old.LegacyRedirectURL
			entry.Attempts = old.Attempts
			if (old.Status == StatusSucceeded || old.Status == StatusExisting) && old.SHA256 == item.SHA256 {
				entry.Status = StatusSkipped
			} else if (old.Status == StatusSucceeded || old.Status == StatusExisting) && !r.options.UpdateExisting {
				entry.Status = StatusFailed
				entry.Error = "source changed; rerun with update-existing to create a new revision"
			}
		}
		report.Items[index] = entry
	}
	report.recount()
	if r.options.DryRun {
		completed := r.options.Now().UTC()
		report.CompletedAt = &completed
		return report, saveReport(r.options.ReportPath, report)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	var reportMu sync.Mutex
	var fatalErr error
	worker := func() {
		defer workers.Done()
		for index := range jobs {
			if runCtx.Err() != nil {
				return
			}
			reportMu.Lock()
			entry := report.Items[index]
			if entry.Status == StatusSkipped {
				reportMu.Unlock()
				continue
			}
			entry.Status = StatusRunning
			started := r.options.Now().UTC()
			entry.StartedAt = &started
			report.Items[index] = entry
			reportMu.Unlock()

			entry = r.processWithRetry(runCtx, plan[index], entry)

			reportMu.Lock()
			finished := r.options.Now().UTC()
			entry.FinishedAt = &finished
			report.Items[index] = entry
			report.recount()
			if err := saveReport(r.options.StatePath, report); err != nil && fatalErr == nil {
				fatalErr = fmt.Errorf("save migration state: %w", err)
				cancel()
			}
			if err := saveReport(r.options.ReportPath, report); err != nil && fatalErr == nil {
				fatalErr = fmt.Errorf("save migration report: %w", err)
				cancel()
			}
			reportMu.Unlock()
		}
	}
	for index := 0; index < r.options.Concurrency; index++ {
		workers.Add(1)
		go worker()
	}
	for index := range plan {
		select {
		case jobs <- index:
		case <-runCtx.Done():
			break
		}
		if runCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	reportMu.Lock()
	completed := r.options.Now().UTC()
	report.CompletedAt = &completed
	report.recount()
	if err := saveReport(r.options.StatePath, report); err != nil && fatalErr == nil {
		fatalErr = fmt.Errorf("save migration state: %w", err)
	}
	if err := saveReport(r.options.ReportPath, report); err != nil && fatalErr == nil {
		fatalErr = fmt.Errorf("save migration report: %w", err)
	}
	reportMu.Unlock()
	return report, fatalErr
}

func (r *Runner) processWithRetry(ctx context.Context, item Item, entry ReportItem) ReportItem {
	for attempt := 0; attempt < r.options.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * 250 * time.Millisecond
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
			select {
			case <-ctx.Done():
				entry.Status = StatusFailed
				entry.Error = ctx.Err().Error()
				return entry
			case <-time.After(delay):
			}
		}
		entry.Attempts++
		err := r.processOnce(ctx, item, &entry)
		if err == nil {
			entry.Error = ""
			return entry
		}
		entry.Error = err.Error()
		if ctx.Err() != nil {
			break
		}
	}
	entry.Status = StatusFailed
	return entry
}

func (r *Runner) processOnce(ctx context.Context, item Item, entry *ReportItem) error {
	info, err := os.Stat(item.SourcePath)
	if err != nil {
		return err
	}
	if info.Size() != item.Size {
		return errors.New("source size changed after migration planning; create a new plan")
	}
	var result UploadResult
	if entry.FlipbookID != "" {
		result.Flipbook, err = r.api.Status(ctx, entry.FlipbookID)
		result.Replay = true
	} else {
		var file *os.File
		file, err = os.Open(item.SourcePath)
		if err == nil {
			initialSlug := item.DesiredSlug
			if item.LegacySlug != "" && item.LegacySlug != item.DesiredSlug {
				initialSlug = item.LegacySlug
			}
			result, err = r.api.Create(ctx, item, initialSlug, file)
			_ = file.Close()
		}
	}
	if err != nil {
		return err
	}
	if result.Flipbook.ID == "" {
		return errors.New("migration API returned an empty flipbook ID")
	}
	entry.FlipbookID = result.Flipbook.ID
	if result.Replay && r.options.UpdateExisting {
		file, openErr := os.Open(item.SourcePath)
		if openErr != nil {
			return openErr
		}
		result, err = r.api.Replace(ctx, result.Flipbook.ID, item, file)
		_ = file.Close()
		if err != nil {
			return err
		}
		result.Replay = true
		result.Flipbook.ID = entry.FlipbookID
	}
	if result.Flipbook.Slug != item.DesiredSlug {
		result.Flipbook, err = r.api.ChangeSlug(ctx, result.Flipbook.ID, item.DesiredSlug)
		if err != nil {
			return err
		}
	}
	entry.ViewerURL = r.baseURL + "/v/" + item.DesiredSlug
	if item.LegacySlug != "" && item.LegacySlug != item.DesiredSlug {
		entry.LegacyRedirectURL = r.baseURL + "/v/" + item.LegacySlug
	}
	if item.Publish {
		revisionID := result.RevisionID
		if _, err := r.waitReady(ctx, result.Flipbook.ID, revisionID); err != nil {
			return err
		}
		if _, err := r.api.Publish(ctx, result.Flipbook.ID, item.Visibility); err != nil {
			return err
		}
	} else if result.Flipbook.Status == "error" {
		return errors.New("conversion failed: " + result.Flipbook.Error)
	}
	if result.Replay && !r.options.UpdateExisting {
		entry.Status = StatusExisting
	} else {
		entry.Status = StatusSucceeded
	}
	return nil
}

func (r *Runner) waitReady(ctx context.Context, flipbookID, revisionID string) (Flipbook, error) {
	if r.options.WaitTimeout == 0 {
		return Flipbook{}, errors.New("publication was requested without a wait timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, r.options.WaitTimeout)
	defer cancel()
	ticker := time.NewTicker(r.options.PollInterval)
	defer ticker.Stop()
	for {
		status, err := r.api.Status(ctx, flipbookID)
		if err == nil {
			revisionReady := revisionID == "" || status.CurrentRevisionID == "" || status.CurrentRevisionID == revisionID
			if status.Status == "ready" && revisionReady {
				return status, nil
			}
			if status.Status == "error" {
				return status, errors.New("conversion failed: " + status.Error)
			}
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return Flipbook{}, fmt.Errorf("wait for conversion: %w", err)
			}
			return Flipbook{}, fmt.Errorf("wait for conversion: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func loadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	if report.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported migration state schema %d", report.SchemaVersion)
	}
	return &report, nil
}

func saveReport(path string, report *Report) error {
	if path == "" || path == "-" {
		return nil
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".migration-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0644); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(name, path)
	}
	return nil
}
