// Package ingest finds work: an *arr webhook for immediacy, a daily scan as the safety
// net (plan.md 13).
//
// There is deliberately no filesystem watcher: on NFS, inotify only reports writes made
// through this client's own mount, so imports done elsewhere are invisible to it.
package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/store"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/fs_mock.go -pkg mock . FS
//go:generate go run -mod=mod github.com/matryer/moq -out mock/fingerprinter_mock.go -pkg mock . Fingerprinter
//go:generate go run -mod=mod github.com/matryer/moq -out mock/analysis_store_mock.go -pkg mock . AnalysisStore
//go:generate go run -mod=mod github.com/matryer/moq -out mock/scan_store_mock.go -pkg mock . ScanStore
//go:generate go run -mod=mod github.com/matryer/moq -out mock/webhook_store_mock.go -pkg mock . WebhookStore
//go:generate go run -mod=mod github.com/matryer/moq -out mock/file_analyzer_mock.go -pkg mock . FileAnalyzer

// Sentinel errors. Everything else is wrapped.
var (
	// ErrOutsideRoots is plan.md 21's path validation: a path under no
	// configured root is not something ingest will touch, whatever asked for it.
	ErrOutsideRoots = errors.New("ingest: path is not under any enabled root")

	// ErrUnknownWebhook means no instance owns that webhook id.
	ErrUnknownWebhook = errors.New("ingest: unknown webhook id")

	// ErrNotAFile is a directory or a special file reaching per-file analysis.
	ErrNotAFile = errors.New("ingest: not a regular file")

	// ErrRootUnreadable is a root that cannot be stat'ed. An unmounted NFS export looks
	// exactly like an empty library, so the scan refuses to prune against it (13.2).
	ErrRootUnreadable = errors.New("ingest: root is unreadable")
)

// StabilityWindow is the guard of plan.md 13.2: a file whose mtime is inside it
// may still be being written, so the next scan takes it instead.
const StabilityWindow = 2 * time.Minute

// FS is the filesystem surface ingest reads. fsx.FS satisfies it.
type FS interface {
	Stat(path string) (fsx.FileInfo, error)
	WalkDir(root string, fn func(path string, info fsx.FileInfo, err error) error) error
}

// Fingerprinter is the sparse fingerprint of plan.md 12.1.
// *fingerprint.Fingerprinter satisfies it.
type Fingerprinter interface {
	Sparse(path string) (string, error)
}

// AnalysisStore is the persistence one analysed file touches.
type AnalysisStore interface {
	ListRoots(ctx context.Context) ([]domain.Root, error)
	GetSettings(ctx context.Context) (domain.Settings, error)
	GetMediaFileByPath(ctx context.Context, path string) (domain.MediaFile, error)
	UpsertMediaFile(ctx context.Context, m domain.MediaFile) (domain.MediaFile, error)
	UpdateMediaAnalysis(ctx context.Context, u store.AnalysisUpdate) error
	SetMediaStatus(ctx context.Context, id int64, status domain.MediaStatus, lastError string) error
	EnqueueJob(ctx context.Context, j domain.Job) (domain.Job, bool, error)
}

// ScanStore is what a whole-root pass needs on top of AnalysisStore.
type ScanStore interface {
	GetSettings(ctx context.Context) (domain.Settings, error)
	ListRoots(ctx context.Context) ([]domain.Root, error)
	GetRoot(ctx context.Context, id int64) (domain.Root, error)
	ListMediaStatsByRoot(ctx context.Context, rootID int64) ([]store.MediaStat, error)
	MarkMediaMissing(ctx context.Context, ids []int64) (int64, error)
}

// WebhookStore is the lookup one *arr event needs.
type WebhookStore interface {
	GetArrInstanceByWebhookID(ctx context.Context, webhookID string) (domain.ArrInstance, error)
	ListArrPathMappings(ctx context.Context, arrInstanceID int64) ([]domain.PathMapping, error)
	ListRoots(ctx context.Context) ([]domain.Root, error)
	GetSettings(ctx context.Context) (domain.Settings, error)
	GetMediaFileByPath(ctx context.Context, path string) (domain.MediaFile, error)
	MarkMediaMissing(ctx context.Context, ids []int64) (int64, error)
}

// FileAnalyzer is the per-file half of the pipeline, so the scan and the
// webhook can be tested without a probe. *Analyzer satisfies it.
type FileAnalyzer interface {
	AnalyzeIn(ctx context.Context, path string, env Env) (Result, error)
}

// Env is the per-pass context an analysis needs, loaded once per scan rather than once
// per file.
type Env struct {
	Roots    []domain.Root
	Settings domain.Settings
	Origin   domain.JobOrigin

	// The movie or series id a webhook named, so the rescan of 16.2 knows what to name.
	// A scan leaves it nil and keeps whatever the row already carries.
	ArrEntityID *int64
}
