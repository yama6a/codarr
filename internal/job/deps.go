// Package job is the queue: one worker goroutine, the job state machine, crash
// recovery, and the bulk operations that fill it, all behind narrow seams (2.2).
package job

import (
	"context"
	"os"
	"time"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fingerprint"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/metrics"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/promote"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/job_mock.go -pkg mock . Store Prober Encoder Promoter FS Fingerprinter Notifier Hardware Analyzer

// Store is the persistence this package uses, split by concern so no single
// interface grows into a second copy of store.Store.
type Store interface {
	QueueStore
	MediaStore
	SettingsStore
	ThroughputStore
}

// QueueStore is the queue itself.
type QueueStore interface {
	EnqueueJob(ctx context.Context, j domain.Job) (domain.Job, bool, error)
	GetJob(ctx context.Context, id int64) (domain.Job, error)
	ActiveJobForMedia(ctx context.Context, mediaFileID int64) (domain.Job, bool, error)
	ClaimNextJob(ctx context.Context) (domain.Job, bool, error)
	SetJobState(ctx context.Context, id int64, state domain.JobState) error
	SetJobBlockedBy(ctx context.Context, id int64, blockedBy string) error
	UpdateJobExecution(ctx context.Context, u store.ExecutionUpdate) error
	UpdateJobProgress(ctx context.Context, id int64, pct, speed, fps float64, estimatedSeconds int) error
	UpdateJobTransform(ctx context.Context, id int64, t domain.TransformRecord) error
	FailJob(ctx context.Context, id int64, code domain.FailureCode, message, stderrTail string) error
	CancelJob(ctx context.Context, id int64) error
	RestartJob(ctx context.Context, id int64) (domain.Job, error)
	RequeueInterruptedJob(ctx context.Context, id int64) (store.SweepResult, error)
	SweepInterruptedJobs(ctx context.Context) ([]store.SweepResult, error)
	CountJobsByState(ctx context.Context) (map[domain.JobState]int, error)
}

// MediaStore is the library rows a job reads and writes.
type MediaStore interface {
	GetMediaFile(ctx context.Context, id int64) (domain.MediaFile, error)
	ListMediaFiles(ctx context.Context, f store.MediaFilter) ([]domain.MediaFile, int, error)
	SetMediaStatus(ctx context.Context, id int64, status domain.MediaStatus, lastError string) error
	RecordPromotion(ctx context.Context, u store.PromotionUpdate) error
	ListRoots(ctx context.Context) ([]domain.Root, error)
}

// SettingsStore carries the pause flag, the temp directory and the full-hash
// switch.
type SettingsStore interface {
	GetSettings(ctx context.Context) (domain.Settings, error)
	UpdateSettings(ctx context.Context, s domain.Settings) error
}

// ThroughputStore is the rolling average behind every duration estimate (14.3).
type ThroughputStore interface {
	GetThroughputStat(ctx context.Context, kind domain.Kind, encoder, resolution string) (domain.ThroughputStat, error)
	UpsertThroughputStat(ctx context.Context, s domain.ThroughputStat) error
}

// Prober is ffprobe. internal/ffprobe.CLI satisfies it.
type Prober interface {
	Probe(ctx context.Context, path string) (*ffprobe.Result, error)
}

// Encoder runs one ffmpeg invocation. ffmpeg.Runner satisfies it, and so does
// ffmpeg.Encoder, which is the same method.
type Encoder interface {
	Run(ctx context.Context, args []string, progress func(p ffmpeg.Progress)) (ffmpeg.RunResult, error)
}

// NewEncoder builds the runner for one invocation, which cannot be a singleton
// because out_time becomes a percentage of the probed duration (14.3).
type NewEncoder func(duration time.Duration) Encoder

// Promoter is preflight, verification and the irreversible replace.
// promote.Promoter satisfies it.
type Promoter interface {
	Preflight(req promote.PreflightRequest) (promote.Staging, error)
	Verify(ctx context.Context, req promote.Request) ([]string, error)
	Promote(ctx context.Context, req promote.Request) (promote.Result, error)
	Sweep(ctx context.Context, roots, claimed []string) ([]string, error)
}

// FS is the filesystem this package touches: staging files it deletes and the
// temp directory the sample probe writes into.
type FS interface {
	Stat(path string) (fsx.FileInfo, error)
	Remove(path string) error
	MkdirAll(path string, mode os.FileMode) error
}

// Fingerprinter is the file identity of plan.md 12.1, which is what tells an
// intact source apart from an output that already landed.
type Fingerprinter interface {
	Sparse(path string) (string, error)
}

// Notifier is the Plex and *arr fan-out of plan.md 15.2 step 10, called directly
// only for a promotion that completed during a crash (19.2).
type Notifier interface {
	NotifyPromoted(ctx context.Context, path string) error
}

// Hardware is the probed encoder capability of plan.md 10; it answers what the
// silicon and driver can do and makes none of the decisions itself.
type Hardware interface {
	Capabilities(ctx context.Context) (hardware.Capabilities, error)
}

// The concrete types cmd/codarr wires in, asserted here rather than in a test so
// a change breaks the build at the seam instead of at the wiring.
var (
	_ Store         = store.Store(nil)
	_ Prober        = (*ffprobe.CLI)(nil)
	_ Encoder       = (*ffmpeg.Runner)(nil)
	_ Promoter      = (*promote.Promoter)(nil)
	_ FS            = fsx.FS(nil)
	_ Fingerprinter = (*fingerprint.Fingerprinter)(nil)
	_ Hardware      = (*hardware.Prober)(nil)
	_ Metrics       = (*metrics.Metrics)(nil)
)

// Analyzer re-probes one file and recomputes its plan against the current policy;
// internal/ingest owns the analysis half of the bulk operations of plan.md 19.
type Analyzer interface {
	Analyze(ctx context.Context, m domain.MediaFile) (domain.MediaFile, error)
}
