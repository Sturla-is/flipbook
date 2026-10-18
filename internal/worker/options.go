package worker

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/models"
)

type WebhookSecretResolver func(subscription *models.WebhookSubscription) (string, error)

type Options struct {
	WorkerID           string
	WorkerCount        int
	WebhookWorkerCount int
	JobTypes           []models.JobType

	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	PollInterval      time.Duration
	ScheduleInterval  time.Duration
	CompletionTimeout time.Duration

	MaxAttempts     int
	RetryBackoff    time.Duration
	MaxRetryBackoff time.Duration

	Webhooks          config.WebhookConfig
	WebhookHTTPClient *http.Client
	WebhookSecret     WebhookSecretResolver
	Logger            *log.Logger
	Now               func() time.Time
}

func DefaultOptions() Options {
	hostname, _ := os.Hostname()
	return Options{
		WorkerID:           hostname + "-" + uuid.NewString(),
		WorkerCount:        2,
		WebhookWorkerCount: 1,
		JobTypes: []models.JobType{
			models.JobConvert, models.JobBackup, models.JobPublish, models.JobUnpublish,
		},
		LeaseDuration:     time.Minute,
		HeartbeatInterval: 15 * time.Second,
		PollInterval:      500 * time.Millisecond,
		ScheduleInterval:  30 * time.Second,
		CompletionTimeout: 10 * time.Second,
		MaxAttempts:       5,
		RetryBackoff:      30 * time.Second,
		MaxRetryBackoff:   time.Hour,
		Webhooks: config.WebhookConfig{
			TimeoutSeconds:       15,
			MaxAttempts:          8,
			RetryBackoffSeconds:  30,
			MaxResponseBodyBytes: 65536,
		},
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func OptionsFromConfig(conversion config.ConversionConfig, webhooks config.WebhookConfig) Options {
	options := DefaultOptions()
	options.WorkerCount = conversion.WorkerCount
	options.LeaseDuration = time.Duration(conversion.JobLeaseSeconds) * time.Second
	options.HeartbeatInterval = time.Duration(conversion.JobHeartbeatSeconds) * time.Second
	options.MaxAttempts = conversion.MaxJobAttempts
	options.RetryBackoff = time.Duration(conversion.RetryBackoffSeconds) * time.Second
	options.MaxRetryBackoff = time.Duration(conversion.MaxRetryBackoffSeconds) * time.Second
	options.Webhooks = webhooks
	return options
}

func normalizeOptions(options Options) Options {
	defaults := DefaultOptions()
	if options.WorkerID == "" {
		options.WorkerID = defaults.WorkerID
	}
	if options.WorkerCount == 0 {
		options.WorkerCount = defaults.WorkerCount
	}
	if options.WebhookWorkerCount == 0 {
		options.WebhookWorkerCount = defaults.WebhookWorkerCount
	}
	if options.JobTypes == nil {
		options.JobTypes = defaults.JobTypes
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = defaults.LeaseDuration
	}
	if options.HeartbeatInterval == 0 {
		options.HeartbeatInterval = defaults.HeartbeatInterval
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaults.PollInterval
	}
	if options.ScheduleInterval == 0 {
		options.ScheduleInterval = defaults.ScheduleInterval
	}
	if options.CompletionTimeout == 0 {
		options.CompletionTimeout = defaults.CompletionTimeout
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = defaults.MaxAttempts
	}
	if options.RetryBackoff == 0 {
		options.RetryBackoff = defaults.RetryBackoff
	}
	if options.MaxRetryBackoff == 0 {
		options.MaxRetryBackoff = defaults.MaxRetryBackoff
	}
	if options.Webhooks.TimeoutSeconds == 0 {
		options.Webhooks.TimeoutSeconds = defaults.Webhooks.TimeoutSeconds
	}
	if options.Webhooks.MaxAttempts == 0 {
		options.Webhooks.MaxAttempts = defaults.Webhooks.MaxAttempts
	}
	if options.Webhooks.RetryBackoffSeconds == 0 {
		options.Webhooks.RetryBackoffSeconds = defaults.Webhooks.RetryBackoffSeconds
	}
	if options.Webhooks.MaxResponseBodyBytes == 0 {
		options.Webhooks.MaxResponseBodyBytes = defaults.Webhooks.MaxResponseBodyBytes
	}
	if options.Now == nil {
		options.Now = defaults.Now
	}
	return options
}

func validateOptions(options Options) error {
	if options.WorkerID == "" || options.WorkerCount < 1 || options.WorkerCount > 100 {
		return errors.New("worker id and a worker count between 1 and 100 are required")
	}
	if options.WebhookWorkerCount < 1 || options.WebhookWorkerCount > 100 {
		return errors.New("webhook worker count must be between 1 and 100")
	}
	if options.LeaseDuration <= 0 || options.HeartbeatInterval <= 0 || options.HeartbeatInterval >= options.LeaseDuration {
		return errors.New("heartbeat must be positive and shorter than the job lease")
	}
	if options.PollInterval <= 0 || options.ScheduleInterval <= 0 || options.CompletionTimeout <= 0 {
		return errors.New("poll, schedule, and completion intervals must be positive")
	}
	if options.MaxAttempts < 1 || options.RetryBackoff <= 0 || options.MaxRetryBackoff < options.RetryBackoff {
		return errors.New("job attempts and retry backoff are invalid")
	}
	if len(options.JobTypes) == 0 {
		return errors.New("at least one durable job type is required")
	}
	seen := make(map[models.JobType]bool, len(options.JobTypes))
	for _, jobType := range options.JobTypes {
		switch jobType {
		case models.JobConvert, models.JobBackup, models.JobPublish, models.JobUnpublish:
		default:
			return errors.New("unsupported durable job type: " + string(jobType))
		}
		if seen[jobType] {
			return errors.New("duplicate durable job type: " + string(jobType))
		}
		seen[jobType] = true
	}
	if options.Webhooks.TimeoutSeconds <= 0 || options.Webhooks.MaxAttempts <= 0 ||
		options.Webhooks.RetryBackoffSeconds <= 0 || options.Webhooks.MaxResponseBodyBytes < 0 {
		return errors.New("webhook timeout, attempts, backoff, and response limit are invalid")
	}
	return nil
}
