package models

import "time"

type JobType string

const (
	JobConvert   JobType = "convert"
	JobPublish   JobType = "publish"
	JobUnpublish JobType = "unpublish"
	JobImport    JobType = "import"
	JobBackup    JobType = "backup"
	JobDelete    JobType = "delete"
	JobWebhook   JobType = "webhook"
	JobNotify    JobType = "notify"
)

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobRetry     JobStatus = "retry"
	JobSucceeded JobStatus = "succeeded"
	JobDead      JobStatus = "dead"
	JobCancelled JobStatus = "cancelled"
)

type Job struct {
	ID              string         `bson:"_id" json:"id"`
	WorkspaceID     string         `bson:"workspace_id" json:"workspace_id"`
	FlipbookID      string         `bson:"flipbook_id,omitempty" json:"flipbook_id,omitempty"`
	RevisionID      string         `bson:"revision_id,omitempty" json:"revision_id,omitempty"`
	Type            JobType        `bson:"type" json:"type"`
	Status          JobStatus      `bson:"status" json:"status"`
	Priority        int            `bson:"priority" json:"priority"`
	Payload         map[string]any `bson:"payload,omitempty" json:"payload,omitempty"`
	IdempotencyKey  string         `bson:"idempotency_key,omitempty" json:"idempotency_key,omitempty"`
	RunAt           time.Time      `bson:"run_at" json:"run_at"`
	LeaseOwner      string         `bson:"lease_owner,omitempty" json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time     `bson:"lease_expires_at,omitempty" json:"lease_expires_at,omitempty"`
	HeartbeatAt     *time.Time     `bson:"heartbeat_at,omitempty" json:"heartbeat_at,omitempty"`
	Attempts        int            `bson:"attempts" json:"attempts"`
	MaxAttempts     int            `bson:"max_attempts" json:"max_attempts"`
	Progress        int            `bson:"progress" json:"progress"`
	ProgressMessage string         `bson:"progress_message,omitempty" json:"progress_message,omitempty"`
	CancelRequested bool           `bson:"cancel_requested" json:"cancel_requested"`
	LastError       string         `bson:"last_error,omitempty" json:"last_error,omitempty"`
	CreatedAt       time.Time      `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time      `bson:"updated_at" json:"updated_at"`
	StartedAt       *time.Time     `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt     *time.Time     `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
}

func (j Job) LeaseExpired(now time.Time) bool {
	return j.Status == JobRunning && j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt)
}

func RetryDelay(attempts int, base, maximum time.Duration) time.Duration {
	if base <= 0 {
		base = 30 * time.Second
	}
	if maximum <= 0 {
		maximum = time.Hour
	}
	if attempts < 1 {
		attempts = 1
	}
	delay := base
	for i := 1; i < attempts; i++ {
		if delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

type QueueMetrics struct {
	Queued         int64      `bson:"queued" json:"queued"`
	Running        int64      `bson:"running" json:"running"`
	Retry          int64      `bson:"retry" json:"retry"`
	Dead           int64      `bson:"dead" json:"dead"`
	OldestQueuedAt *time.Time `bson:"oldest_queued_at" json:"oldest_queued_at,omitempty"`
}
