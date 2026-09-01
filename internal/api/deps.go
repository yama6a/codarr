// Package api implements the generated StrictServerInterface by hand.
//
// Handlers are thin: they validate, map and delegate. Everything that decides
// anything lives in the package being called. Nothing in here is generated, and
// nothing generated lives here, so regenerating api/ can never overwrite handler
// logic.
package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/api_mock.go -pkg mock . Store Queue Analyzer Scanner Webhooks Hardware Fingerprinter FS Pinger PlexAuth PlexClient ArrClient Metrics

// Store is the persistence the API reads and writes, split by concern so no
// handler depends on the 74-method store.Store. The same concrete store
// satisfies all of it.
type Store interface {
	SettingsStore
	PlexStore
	ArrStore
	RootStore
	MediaStore
	JobStore
	EventStore
	HardwareStore
}

// SettingsStore is the single settings row.
type SettingsStore interface {
	GetSettings(ctx context.Context) (domain.Settings, error)
	UpdateSettings(ctx context.Context, s domain.Settings) error
}

// PlexStore is the single Plex server and its path mappings.
type PlexStore interface {
	GetPlexConfig(ctx context.Context) (domain.PlexConfig, error)
	UpdatePlexConfig(ctx context.Context, c domain.PlexConfig) error
	SetPlexTestResult(ctx context.Context, at time.Time, result string) error
	ListPlexPathMappings(ctx context.Context) ([]domain.PathMapping, error)
	ReplacePlexPathMappings(ctx context.Context, m []domain.PathMapping) error
}

// ArrStore is every Radarr and Sonarr instance.
type ArrStore interface {
	CreateArrInstance(ctx context.Context, a domain.ArrInstance) (domain.ArrInstance, error)
	UpdateArrInstance(ctx context.Context, a domain.ArrInstance) error
	DeleteArrInstance(ctx context.Context, id int64) error
	GetArrInstance(ctx context.Context, id int64) (domain.ArrInstance, error)
	GetArrInstanceByWebhookID(ctx context.Context, webhookID string) (domain.ArrInstance, error)
	ListArrInstances(ctx context.Context) ([]domain.ArrInstance, error)
	SetArrTestResult(ctx context.Context, id int64, at time.Time, result string) error
	ListArrPathMappings(ctx context.Context, arrInstanceID int64) ([]domain.PathMapping, error)
	ReplaceArrPathMappings(ctx context.Context, arrInstanceID int64, m []domain.PathMapping) error
}

// RootStore is the watch roots.
type RootStore interface {
	CreateRoot(ctx context.Context, r domain.Root) (domain.Root, error)
	DeleteRoot(ctx context.Context, id int64) error
	GetRoot(ctx context.Context, id int64) (domain.Root, error)
	ListRoots(ctx context.Context) ([]domain.Root, error)
}

// MediaStore is the library table.
type MediaStore interface {
	GetMediaFile(ctx context.Context, id int64) (domain.MediaFile, error)
	ListMediaFiles(ctx context.Context, f store.MediaFilter) ([]domain.MediaFile, int, error)
	SetMediaIgnored(ctx context.Context, id int64, ignored bool) error
	SetMediaIntegrity(ctx context.Context, id int64, fingerprint, fullHash string, at time.Time) error
	CountMediaByStatus(ctx context.Context) (map[domain.MediaStatus]int, error)
	CountMediaByPlanKind(ctx context.Context) (map[domain.Kind]int, error)
}

// JobStore is the queue as the API reads it. Every mutation goes through Queue.
type JobStore interface {
	GetJob(ctx context.Context, id int64) (domain.Job, error)
	ListJobs(ctx context.Context, f store.JobFilter) ([]domain.Job, int, error)
	CountJobsByState(ctx context.Context) (map[domain.JobState]int, error)
	Stats(ctx context.Context) (store.Stats, error)
}

// EventStore is the log view of plan.md 18.5.
type EventStore interface {
	ListEvents(ctx context.Context, f store.EventFilter) ([]domain.Event, error)
}

// HardwareStore is the cached capability probe.
type HardwareStore interface {
	ListHWCapabilities(ctx context.Context) ([]domain.HWCapability, error)
}

// Queue is the job service. *job.Service satisfies it.
type Queue interface {
	Enqueue(ctx context.Context, mediaFileID int64, origin domain.JobOrigin) (job.EnqueueResult, error)
	Cancel(ctx context.Context, jobID int64) error
	Restart(ctx context.Context, jobID int64) (domain.Job, error)
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Paused(ctx context.Context) (bool, error)
	RecheckAll(ctx context.Context, confirm bool) (job.RecheckResult, error)
	Recheck(ctx context.Context, req job.Recheck) (job.RecheckResult, error)
	SpaceSweepPreview(ctx context.Context) (job.SpaceSweepPreview, error)
	SpaceSweepRun(ctx context.Context, ids []int64, confirm bool) (job.SpaceSweepPreview, error)
}

// Analyzer re-probes and re-plans one file. *ingest.Analyzer satisfies it.
type Analyzer interface {
	Analyze(ctx context.Context, path string, origin domain.JobOrigin) (ingest.Result, error)
}

// Scanner walks a root now. *ingest.Scanner satisfies it.
type Scanner interface {
	ScanRoot(ctx context.Context, rootID int64) (ingest.ScanReport, error)
}

// Webhooks turns one parsed *arr event into work. *ingest.Webhook satisfies it.
type Webhooks interface {
	Handle(ctx context.Context, webhookID string, ev ingest.Event) (ingest.Ack, error)
}

// Hardware is the capability probe of plan.md 10.1: cache-first on read, forced
// on the UI's re-probe button. *hardware.Prober satisfies it.
type Hardware interface {
	Capabilities(ctx context.Context) (hardware.Capabilities, error)
	Probe(ctx context.Context) (hardware.Capabilities, error)
}

// Fingerprinter is the file identity of plan.md 12, behind verify-integrity.
type Fingerprinter interface {
	Sparse(path string) (string, error)
	Full(path string) (string, error)
}

// FS is the one filesystem call the API makes: stat, to report the size an
// integrity check saw.
type FS interface {
	Stat(path string) (fsx.FileInfo, error)
}

// PlexAuth is the plex.tv PIN flow. *plex.Auth satisfies it.
type PlexAuth interface {
	CreatePin(ctx context.Context, clientIdentifier string) (plex.Pin, error)
	CheckPin(ctx context.Context, clientIdentifier string, id int64) (plex.Pin, error)
	AuthURL(clientIdentifier, code string) string
}

// PlexClient is the configured Plex server. *plex.Client satisfies it.
type PlexClient interface {
	Test(ctx context.Context) plex.TestResult
	Sections(ctx context.Context) ([]plex.Section, error)
	Resolve(ctx context.Context, localPath string) (plex.Target, error)
}

// ArrClient is one Radarr or Sonarr. *arr.API satisfies it.
type ArrClient interface {
	Test(ctx context.Context) arr.TestResult
	RootFolders(ctx context.Context) ([]arr.RootFolder, error)
}

// PlexFactory builds a client from the stored configuration. It is a factory
// rather than a value because the base URL and token are edited at runtime and
// a cached client would keep talking to the old server.
type PlexFactory func(ctx context.Context) (PlexClient, error)

// ArrFactory builds a client for one instance, for the same reason.
type ArrFactory func(ctx context.Context, instance domain.ArrInstance) (ArrClient, error)

// Metrics is the subset of the Prometheus surface the API touches. The worker's
// own transitions are recorded by the worker.
type Metrics interface {
	JobObserved(state domain.JobState, kind domain.Kind, origin domain.JobOrigin)
	Error(category string)
}

// Pinger answers whether the database is reachable, behind /readyz and
// GET /api/ready. *sql.DB satisfies it.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Build identifies the running binary for GET /api/version.
type Build struct {
	Version   string
	Commit    string
	BuiltAt   time.Time
	GoVersion string
}

// Deps is everything the API needs. Nothing is instantiated internally; see
// CLAUDE.md, it is all wired in cmd/codarr/main.go.
type Deps struct {
	Store         Store
	DB            Pinger
	Queue         Queue
	Analyzer      Analyzer
	Scanner       Scanner
	Webhooks      Webhooks
	Hardware      Hardware
	Fingerprinter Fingerprinter
	FS            FS
	PlexAuth      PlexAuth
	PlexFactory   PlexFactory
	ArrFactory    ArrFactory
	Metrics       Metrics
	Clock         clock.Clock
	Logger        *slog.Logger
	Build         Build

	// FfmpegVersion is reported by GET /api/version and GET /api/hardware. It is
	// read once at startup, since the binary cannot change under itself.
	FfmpegVersion string
}
