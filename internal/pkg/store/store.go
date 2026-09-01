package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// ErrNotFound is returned when a row a caller named does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrInvalidFailure is returned when a job is failed without both halves of plan.md 19.1,
// which the store refuses to write because it would be a bug.
var ErrInvalidFailure = errors.New("store: failure code and message are both required")

// timeLayout is fixed width on purpose; see the package comment.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// MediaSort names a sortable column of the library table.
type MediaSort string

const (
	SortPath       MediaSort = "path"
	SortSize       MediaSort = "size"
	SortStatus     MediaSort = "status"
	SortPlanKind   MediaSort = "plan_kind"
	SortVideoCodec MediaSort = "video_codec"
	SortBitrate    MediaSort = "bitrate"
	SortUpdatedAt  MediaSort = "updated_at"
	SortProvenance MediaSort = "provenance"
)

// MediaFilter is the server-side filter, sort and pagination of plan.md 18.2.
type MediaFilter struct {
	Query          string
	Status         []domain.MediaStatus
	PlanKind       []domain.Kind
	VideoCodec     []string
	ArrInstanceID  *int64
	Provenance     []domain.Provenance
	IncludeIgnored bool

	Sort       MediaSort
	Descending bool
	Limit      int
	Offset     int
}

// JobFilter selects jobs for the queue and history views.
type JobFilter struct {
	State       []domain.JobState
	MediaFileID *int64
	Limit       int
	Offset      int
}

// EventFilter is the cursor read behind GET /api/events.
type EventFilter struct {
	Level    []string
	Category []string
	SinceID  int64
	Limit    int
}

// AnalysisUpdate carries everything one analysis pass learned about a file; provenance
// is absent on purpose, being derived from the fingerprints (plan.md 12).
type AnalysisUpdate struct {
	MediaFileID int64

	SizeBytes int64
	MTime     int64
	NLink     int

	Fingerprint     string
	FingerprintAlgo string

	ProbeJSON     string
	MediaInfoJSON string

	Plan        *domain.Plan
	PlanKind    domain.Kind
	PlanReasons []string

	Container       string
	VideoCodec      string
	VideoProfile    string
	VideoLevel      string
	VideoBitrate    int
	VideoBitrateSrc domain.BitrateSource
	IsHDR           bool

	CodarrTagged     bool
	CodarrPolicyHash string

	Status     domain.MediaStatus
	LastError  string
	AnalyzedAt time.Time
}

// PromotionUpdate is step 9 of plan.md 15.2, setting size, mtime and fingerprint to the
// output's own values so the next scan does not re-probe Codarr's work.
type PromotionUpdate struct {
	JobID       int64
	MediaFileID int64

	OutputFingerprint string
	OutputFullHash    string
	OutputSize        int64
	OutputMTime       int64
	PolicyHash        string

	Transform     domain.TransformRecord
	ActualSeconds int
	PromotedAt    time.Time
}

// ExecutionUpdate records what the worker actually did once a job starts.
type ExecutionUpdate struct {
	JobID            int64
	StagingPath      string
	UsedTempDir      bool
	FfmpegArgv       []string
	EncoderUsed      domain.Encoder
	DecodePath       domain.DecodePath
	FellBack         bool
	FallbackReason   string
	SourceSize       int64
	EstimatedSeconds int

	// ffmpeg's own last out_time, which verification needs for a legacy container
	// whose header lies about duration (plan.md 14.3, 15.3), restart included.
	FinalOutTimeUS int64
}

// MediaStat is the cheap projection the scheduled scan diffs against the filesystem
// (plan.md 13.2); whole rows would read the entire library into memory.
type MediaStat struct {
	ID        int64
	Path      string
	SizeBytes int64
	MTime     int64
	Status    domain.MediaStatus
	Ignored   bool
}

// SweepAction is what SweepInterruptedJobs did with one job.
type SweepAction string

const (
	// SweepRequeued means the job went back to queued at the front of the
	// queue with attempt incremented.
	SweepRequeued SweepAction = "requeued"
	// SweepFailed means the attempt cap was reached and the job is now
	// failed with failure_code 'interrupted'.
	SweepFailed SweepAction = "failed"
	// SweepNeedsCheck leaves the job as found: promoting and awaiting_stream_end
	// need the filesystem to decide what happened (plan.md 19.2).
	SweepNeedsCheck SweepAction = "needs_consistency_check"
)

// SweepResult is one job's outcome from the startup sweep.
type SweepResult struct {
	JobID       int64
	MediaFileID int64
	FoundState  domain.JobState
	Action      SweepAction
	Attempt     int
	StagingPath string
}

// Stats feeds the dashboard totals of plan.md 18.1.
type Stats struct {
	FilesDone     int
	BytesIn       int64
	BytesOut      int64
	BytesSaved    int64
	EncodeSeconds int64
}

// Store is the whole persistence surface. It is one interface rather than nine
// because there is one database, one transaction boundary and one mock.
//
//go:generate go run -mod=mod github.com/matryer/moq -out mock/store_mock.go -pkg mock . Store
type Store interface { //nolint:interfacebloat // one database, one mock; splitting it would only move the seams
	// Settings. GetSettings returns ErrNotFound until EnsureSettings has run;
	// the schema ships no default row.
	EnsureSettings(ctx context.Context, defaults domain.Settings) error
	GetSettings(ctx context.Context) (domain.Settings, error)
	UpdateSettings(ctx context.Context, s domain.Settings) error

	// Plex.
	GetPlexConfig(ctx context.Context) (domain.PlexConfig, error)
	UpdatePlexConfig(ctx context.Context, c domain.PlexConfig) error
	SetPlexTestResult(ctx context.Context, at time.Time, result string) error
	ListPlexPathMappings(ctx context.Context) ([]domain.PathMapping, error)
	ReplacePlexPathMappings(ctx context.Context, m []domain.PathMapping) error

	// *arr instances.
	CreateArrInstance(ctx context.Context, a domain.ArrInstance) (domain.ArrInstance, error)
	UpdateArrInstance(ctx context.Context, a domain.ArrInstance) error
	DeleteArrInstance(ctx context.Context, id int64) error
	GetArrInstance(ctx context.Context, id int64) (domain.ArrInstance, error)
	GetArrInstanceByWebhookID(ctx context.Context, webhookID string) (domain.ArrInstance, error)
	ListArrInstances(ctx context.Context) ([]domain.ArrInstance, error)
	SetArrTestResult(ctx context.Context, id int64, at time.Time, result string) error
	ListArrPathMappings(ctx context.Context, arrInstanceID int64) ([]domain.PathMapping, error)
	ReplaceArrPathMappings(ctx context.Context, arrInstanceID int64, m []domain.PathMapping) error

	// Roots.
	CreateRoot(ctx context.Context, r domain.Root) (domain.Root, error)
	DeleteRoot(ctx context.Context, id int64) error
	GetRoot(ctx context.Context, id int64) (domain.Root, error)
	ListRoots(ctx context.Context) ([]domain.Root, error)
	SetRootEnabled(ctx context.Context, id int64, enabled bool) error

	// Media.
	UpsertMediaFile(ctx context.Context, m domain.MediaFile) (domain.MediaFile, error)
	GetMediaFile(ctx context.Context, id int64) (domain.MediaFile, error)
	GetMediaFileByPath(ctx context.Context, path string) (domain.MediaFile, error)
	ListMediaFiles(ctx context.Context, f MediaFilter) ([]domain.MediaFile, int, error)
	ListMediaStatsByRoot(ctx context.Context, rootID int64) ([]MediaStat, error)
	UpdateMediaAnalysis(ctx context.Context, u AnalysisUpdate) error
	SetMediaStatus(ctx context.Context, id int64, status domain.MediaStatus, lastError string) error
	SetMediaIgnored(ctx context.Context, id int64, ignored bool) error
	MarkMediaMissing(ctx context.Context, ids []int64) (int64, error)
	SetMediaIntegrity(ctx context.Context, id int64, fingerprint, fullHash string, at time.Time) error
	RecordPromotion(ctx context.Context, u PromotionUpdate) error
	CountMediaByStatus(ctx context.Context) (map[domain.MediaStatus]int, error)
	CountMediaByPlanKind(ctx context.Context) (map[domain.Kind]int, error)

	// Jobs.
	EnqueueJob(ctx context.Context, j domain.Job) (domain.Job, bool, error)
	GetJob(ctx context.Context, id int64) (domain.Job, error)
	ListJobs(ctx context.Context, f JobFilter) ([]domain.Job, int, error)
	ActiveJobForMedia(ctx context.Context, mediaFileID int64) (domain.Job, bool, error)
	ClaimNextJob(ctx context.Context) (domain.Job, bool, error)
	SetJobState(ctx context.Context, id int64, state domain.JobState) error
	SetJobBlockedBy(ctx context.Context, id int64, blockedBy string) error
	UpdateJobExecution(ctx context.Context, u ExecutionUpdate) error
	UpdateJobProgress(ctx context.Context, id int64, pct, speed, fps float64, estimatedSeconds int) error
	UpdateJobTransform(ctx context.Context, id int64, t domain.TransformRecord) error
	FailJob(ctx context.Context, id int64, code domain.FailureCode, message, stderrTail string) error
	CancelJob(ctx context.Context, id int64) error
	RestartJob(ctx context.Context, id int64) (domain.Job, error)
	RequeueInterruptedJob(ctx context.Context, id int64) (SweepResult, error)
	SweepInterruptedJobs(ctx context.Context) ([]SweepResult, error)
	CountJobsByState(ctx context.Context) (map[domain.JobState]int, error)
	Stats(ctx context.Context) (Stats, error)

	// Hardware.
	ReplaceHWCapabilities(ctx context.Context, caps []domain.HWCapability) error
	ListHWCapabilities(ctx context.Context) ([]domain.HWCapability, error)

	// Events.
	AppendEvent(ctx context.Context, e domain.Event) (int64, error)
	ListEvents(ctx context.Context, f EventFilter) ([]domain.Event, error)
	PruneEvents(ctx context.Context, now time.Time) (int64, error)

	// Throughput.
	UpsertThroughputStat(ctx context.Context, s domain.ThroughputStat) error
	GetThroughputStat(ctx context.Context, kind domain.Kind, encoder, resolution string) (domain.ThroughputStat, error)
	ListThroughputStats(ctx context.Context) ([]domain.ThroughputStat, error)
}

var _ Store = (*store)(nil)

type store struct {
	db     *DB
	logger *slog.Logger
}

// New returns a Store over db.
//
//nolint:ireturn // consumers hold the interface so they can swap in the generated mock
func New(db *DB, logger *slog.Logger) Store {
	return &store{db: db, logger: logger.With(slog.String("component", "store"))}
}

// write runs fn in a single transaction on the write pool. Nothing inside fn may read
// through the read pool for a value it then writes, or the transaction is not the view.
func (s *store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (s *store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := s.db.write.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}

	return res, nil
}

func (s *store) execOne(ctx context.Context, query string, args ...any) error {
	res, err := s.exec(ctx, query, args...)
	if err != nil {
		return err
	}

	return requireOne(res)
}

func requireOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return ErrNotFound
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}

	return formatTime(*t)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}

	return t.UTC(), nil
}

func scanTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid {
		return time.Time{}, nil
	}

	return parseTime(ns.String)
}

func scanTimePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil //nolint:nilnil // absent timestamp, not an error
	}

	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}

	return v
}

func int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}

	v := n.Int64

	return &v
}

func ptrValue(p *int64) any {
	if p == nil {
		return nil
	}

	return *p
}

func marshalJSON(v any) (any, error) {
	if v == nil {
		return nil, nil //nolint:nilnil // a nil value is a SQL NULL, not a failure
	}

	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}

	return string(b), nil
}

func unmarshalJSON(ns sql.NullString, dst any) error {
	if !ns.Valid || ns.String == "" {
		return nil
	}

	if err := json.Unmarshal([]byte(ns.String), dst); err != nil {
		return fmt.Errorf("unmarshal json: %w", err)
	}

	return nil
}

func marshalStrings(v []string) (any, error) {
	if len(v) == 0 {
		return nil, nil //nolint:nilnil // an empty slice is a SQL NULL, not a failure
	}

	return marshalJSON(v)
}

// conds accumulates a WHERE clause and its bound arguments. Every value reaches the
// query as a placeholder; only column names, which are constants, are concatenated.
type conds struct {
	clauses []string
	args    []any
}

func (c *conds) add(clause string, args ...any) {
	c.clauses = append(c.clauses, clause)
	c.args = append(c.args, args...)
}

func (c *conds) in(column string, values []string) {
	if len(values) == 0 {
		return
	}

	c.clauses = append(c.clauses, column+" IN ("+placeholders(len(values))+")")

	for _, v := range values {
		c.args = append(c.args, v)
	}
}

func (c *conds) where() string {
	if len(c.clauses) == 0 {
		return ""
	}

	return " WHERE " + strings.Join(c.clauses, " AND ")
}

func strs[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}

	return out
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}

	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rows: %w", err)
	}

	return nil
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
