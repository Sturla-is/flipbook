package migration

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/metavert-io/flipbook/internal/config"
)

// Main is the callable Issuu migration entry point. The final CLI wiring can
// dispatch a subcommand with os.Exit(migration.Main(args)) without changing
// this package's API.
func Main(args []string) int {
	return RunCLI(context.Background(), args, os.Stdout, os.Stderr)
}

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("issuu-migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options Options
	var baseURL, apiKey, workspaceID string
	flags.StringVar(&options.Folder, "folder", "", "Folder containing source documents, or the root for relative CSV paths")
	flags.StringVar(&options.CSVFile, "csv", "", "CSV manifest with path, external_id, title, desired_slug, legacy_url, and metadata columns")
	flags.StringVar(&baseURL, "base-url", "", "Flipbook base URL (defaults to application configuration)")
	flags.StringVar(&apiKey, "api-key", "", "Scoped API key (defaults to application configuration; never printed)")
	flags.StringVar(&workspaceID, "workspace", "", "Workspace ID sent as X-Workspace-ID")
	flags.IntVar(&options.Concurrency, "concurrency", 2, "Concurrent imports (1-16)")
	flags.IntVar(&options.MaxAttempts, "max-attempts", 3, "Attempts per item in this run (1-10)")
	flags.StringVar(&options.ReportPath, "report", "issuu-migration-report.json", "Machine-readable JSON report path, or - for stdout only")
	flags.StringVar(&options.StatePath, "state", "", "Resumable JSON state path (defaults to <report>.state.json)")
	flags.BoolVar(&options.Resume, "resume", true, "Skip unchanged successful items from the state file")
	flags.BoolVar(&options.DryRun, "dry-run", false, "Validate and report the plan without uploading")
	flags.BoolVar(&options.UpdateExisting, "update-existing", false, "Create a new revision when an external ID already exists")
	flags.BoolVar(&options.Publish, "publish", false, "Wait for conversion and publish each imported document")
	flags.StringVar(&options.Visibility, "visibility", "public", "Default publication visibility")
	flags.DurationVar(&options.WaitTimeout, "wait-timeout", 15*time.Minute, "Maximum conversion wait per item when publishing")
	flags.DurationVar(&options.PollInterval, "poll-interval", 2*time.Second, "Conversion status polling interval")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: issuu-migrate -folder DIR [-csv manifest.csv] [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if options.Folder == "" && options.CSVFile == "" {
		flags.Usage()
		return 2
	}
	cfg := config.Load()
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	client := NewAPIClient(baseURL, apiKey, workspaceID)
	runner, err := NewRunner(client, baseURL, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	report, err := runner.Run(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	summary := map[string]interface{}{
		"run_id": report.RunID, "report": options.ReportPath, "state": runner.options.StatePath,
		"total": report.Total, "planned": report.Planned, "succeeded": report.Succeeded,
		"existing": report.Existing, "skipped": report.Skipped, "failed": report.Failed,
	}
	_ = json.NewEncoder(stdout).Encode(summary)
	if report.Failed > 0 {
		return 1
	}
	return 0
}
