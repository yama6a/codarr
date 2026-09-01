package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fingerprint"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// Result is what one analysis pass concluded. Everything the UI and the caller
// need is on it, so nothing has to re-read the row to find out what happened.
type Result struct {
	Path        string
	MediaFileID int64

	// Excluded is set when the file never reached the probe.
	Excluded Exclusion

	// Skipped is the section 12 conjunction: tagged, on the current policy, and
	// still byte-identical to what Codarr wrote.
	Skipped    bool
	SkipReason string

	Provenance domain.Provenance
	PlanKind   domain.Kind

	// NeedsIdetSample is the engine asking for an idet run before the scan
	// verdict can be trusted for a legacy codec (6.2).
	NeedsIdetSample bool

	// Conflict is two enabled instances claiming the same root (16.2). The file
	// is still processed; no *arr is notified.
	Conflict *pathmap.Conflict

	JobID  int64
	Queued bool
}

// Analyzer does one file end to end: fingerprint, probe, decide, write the row,
// and enqueue a job when the plan calls for one.
type Analyzer struct {
	fs     FS
	fp     Fingerprinter
	prober ffprobe.Prober
	engine decide.Engine
	store  AnalysisStore
	clock  clock.Clock
	logger *slog.Logger
}

var _ FileAnalyzer = (*Analyzer)(nil)

// NewAnalyzer returns an Analyzer. The decision engine is stateless and
// hard-coded, so it is constructed rather than injected.
func NewAnalyzer(fs FS, fp Fingerprinter, prober ffprobe.Prober, st AnalysisStore,
	clk clock.Clock, logger *slog.Logger,
) *Analyzer {
	return &Analyzer{
		fs:     fs,
		fp:     fp,
		prober: prober,
		engine: decide.New(),
		store:  st,
		clock:  clk,
		logger: logger.With(slog.String("component", "ingest.analyzer")),
	}
}

// Analyze loads the roots and settings itself, for a one-off; a scan uses AnalyzeIn so
// ten thousand files do not re-read the same two rows.
func (a *Analyzer) Analyze(ctx context.Context, path string, origin domain.JobOrigin) (Result, error) {
	env, err := a.Env(ctx, origin)
	if err != nil {
		return Result{}, err
	}

	return a.AnalyzeIn(ctx, path, env)
}

// Env loads the per-pass context once.
func (a *Analyzer) Env(ctx context.Context, origin domain.JobOrigin) (Env, error) {
	roots, err := a.store.ListRoots(ctx)
	if err != nil {
		return Env{}, fmt.Errorf("list roots: %w", err)
	}

	settings, err := a.store.GetSettings(ctx)
	if err != nil {
		return Env{}, fmt.Errorf("get settings: %w", err)
	}

	return Env{Roots: roots, Settings: settings, Origin: origin}, nil
}

// AnalyzeIn is Analyze with the per-pass context already loaded.
func (a *Analyzer) AnalyzeIn(ctx context.Context, path string, env Env) (Result, error) {
	res := Result{Path: path}

	info, excl, err := a.inspect(path)
	if err != nil {
		return res, err
	}

	if excl != NotExcluded {
		res.Excluded = excl

		return res, nil
	}

	att, ok := pathmap.Attribute(env.Roots, path)
	if !ok {
		return res, fmt.Errorf("%w: %s", ErrOutsideRoots, path)
	}

	res.Conflict = att.Conflict

	row, excl, err := a.record(ctx, path, info, att, env)
	if err != nil {
		return res, err
	}

	res.MediaFileID = row.ID

	if excl != NotExcluded {
		res.Excluded = excl

		return res, nil
	}

	res.Provenance = row.Provenance

	probe, err := a.probe(ctx, path, row.ID)
	if err != nil {
		return res, err
	}

	return a.decide(ctx, res, env, row, probe, row.Fingerprint, info)
}

// inspect is the pre-database half: what the file is, and whether one of the
// hard-coded rules of 13.3 already rules it out.
func (a *Analyzer) inspect(path string) (fsx.FileInfo, Exclusion, error) {
	info, err := a.fs.Stat(path)
	if err != nil {
		return fsx.FileInfo{}, NotExcluded, fmt.Errorf("stat %s: %w", path, err)
	}

	if info.IsDir {
		return fsx.FileInfo{}, NotExcluded, fmt.Errorf("%w: %s", ErrNotAFile, path)
	}

	return info, ExcludeFile(path, info.Size), nil
}

// record loads the existing row, honours the per-file ignore list before doing
// any I/O on the file itself, fingerprints it and writes the row back.
func (a *Analyzer) record(ctx context.Context, path string, info fsx.FileInfo,
	att pathmap.Attribution, env Env,
) (domain.MediaFile, Exclusion, error) {
	existing, err := a.store.GetMediaFileByPath(ctx, path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return domain.MediaFile{}, NotExcluded, fmt.Errorf("load media row for %s: %w", path, err)
	}

	// The per-file ignore list of 13.3, checked before the fingerprint so an
	// ignored file costs one query and no reads.
	if existing.Ignored {
		return existing, ExcludedIgnored, nil
	}

	fp, err := a.fp.Sparse(path)
	if err != nil {
		return domain.MediaFile{}, NotExcluded, fmt.Errorf("fingerprint %s: %w", path, err)
	}

	row, err := a.store.UpsertMediaFile(ctx, a.rowFor(path, info, fp, att, existing, env))
	if err != nil {
		return domain.MediaFile{}, NotExcluded, fmt.Errorf("upsert media row for %s: %w", path, err)
	}

	return row, NotExcluded, nil
}

func (a *Analyzer) probe(ctx context.Context, path string, mediaFileID int64) (*ffprobe.Result, error) {
	probe, err := a.prober.Probe(ctx, path)
	if err == nil {
		return probe, nil
	}

	a.fail(ctx, mediaFileID, path, err)

	return nil, fmt.Errorf("probe %s: %w", path, err)
}

// fail records why a file could not be analysed. A failure to record that is
// logged rather than returned: the original error is the interesting one.
func (a *Analyzer) fail(ctx context.Context, mediaFileID int64, path string, cause error) {
	if err := a.store.SetMediaStatus(ctx, mediaFileID, domain.MediaFailed, cause.Error()); err != nil {
		a.logger.Error("could not record an analysis failure",
			slog.String("path", path), slog.String("error", err.Error()))
	}
}

func (a *Analyzer) decide(ctx context.Context, res Result, env Env, row domain.MediaFile,
	probe *ffprobe.Result, fp string, info fsx.FileInfo,
) (Result, error) {
	check := a.engine.CheckSkip(probe, fp, row.CodarrOutputFingerprint)
	res.Skipped, res.SkipReason = check.Skip, check.Reason

	if check.Provenance == domain.ProvenanceModified {
		// plan.md 12: the tag matches but the bytes do not, so something
		// rewrote Codarr's output. Surface it rather than reprocessing quietly.
		a.logger.Warn("file was modified after Codarr wrote it",
			slog.String("path", res.Path), slog.Int64("media_file_id", row.ID))
	}

	analysis, err := a.engine.Plan(probe, decide.Options{Path: res.Path})
	if err != nil {
		a.fail(ctx, row.ID, res.Path, err)

		return res, fmt.Errorf("plan %s: %w", res.Path, err)
	}

	plan := analysis.Plan
	res.PlanKind = plan.Kind
	res.NeedsIdetSample = analysis.NeedsIdetSample

	status := statusFor(check.Skip, plan.Kind)

	if err := a.store.UpdateMediaAnalysis(ctx,
		a.analysisUpdate(row.ID, info, fp, probe, plan, check, status)); err != nil {
		return res, fmt.Errorf("record analysis for %s: %w", res.Path, err)
	}

	if status != domain.MediaAnalyzed {
		return res, nil
	}

	job, created, err := a.store.EnqueueJob(ctx, domain.Job{
		MediaFileID: row.ID,
		Kind:        plan.Kind,
		Origin:      env.Origin,
		Priority:    PriorityFor(plan.Kind, env.Settings.PrioritiseQuickJobs),
		Transform:   decide.NewTransform(probe, plan, 0),
		QueuedAt:    a.clock.Now(),
	})
	if err != nil {
		return res, fmt.Errorf("enqueue %s: %w", res.Path, err)
	}

	res.JobID, res.Queued = job.ID, created

	return res, nil
}

func (a *Analyzer) rowFor(path string, info fsx.FileInfo, fp string,
	att pathmap.Attribution, existing domain.MediaFile, env Env,
) domain.MediaFile {
	now := a.clock.Now()

	row := domain.MediaFile{
		Path:            path,
		ArrInstanceID:   att.ArrInstanceID,
		ArrEntityID:     existing.ArrEntityID,
		SizeBytes:       info.Size,
		MTime:           info.MTime.Unix(),
		NLink:           info.NLink,
		Fingerprint:     fp,
		FingerprintAlgo: fingerprint.Algo,
		Status:          domain.MediaNew,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if att.Root.ID != 0 {
		id := att.Root.ID
		row.RootID = &id
	}

	if env.ArrEntityID != nil {
		row.ArrEntityID = env.ArrEntityID
	}

	if existing.ID != 0 {
		row.CreatedAt = existing.CreatedAt
	}

	return row
}

func (a *Analyzer) analysisUpdate(mediaFileID int64, info fsx.FileInfo, fp string,
	probe *ffprobe.Result, plan domain.Plan, check decide.SkipCheck, status domain.MediaStatus,
) store.AnalysisUpdate {
	bitrate, src := decide.ResolveVideoBitrate(probe)
	policy, _ := probe.Format.Tag(decide.TagPolicy)
	video, _ := probe.PrimaryVideo()

	return store.AnalysisUpdate{
		MediaFileID:      mediaFileID,
		SizeBytes:        info.Size,
		MTime:            info.MTime.Unix(),
		NLink:            info.NLink,
		Fingerprint:      fp,
		FingerprintAlgo:  fingerprint.Algo,
		ProbeJSON:        string(probe.Raw),
		Plan:             &plan,
		PlanKind:         plan.Kind,
		PlanReasons:      plan.Reasons,
		Container:        plan.SourceContainer,
		VideoCodec:       video.CodecName,
		VideoProfile:     video.Profile,
		VideoLevel:       video.LevelString(),
		VideoBitrate:     bitrate,
		VideoBitrateSrc:  src,
		IsHDR:            plan.HDR,
		CodarrTagged:     check.Tagged,
		CodarrPolicyHash: policy,
		Status:           status,
		AnalyzedAt:       a.clock.Now(),
	}
}

// statusFor maps the two verdicts onto a row status. A section 12 skip means
// the file is Codarr's own untouched output, which is 'done', not 'skipped'.
func statusFor(skip bool, kind domain.Kind) domain.MediaStatus {
	switch {
	case skip:
		return domain.MediaDone
	case kind == domain.KindSkip:
		return domain.MediaSkipped
	default:
		return domain.MediaAnalyzed
	}
}

// PriorityFor is plan.md 19's ordering: quick wins clear ahead of encodes.
func PriorityFor(kind domain.Kind, prioritiseQuick bool) int {
	switch kind {
	case domain.KindFull:
		return domain.PriorityFull
	case domain.KindRemux, domain.KindAudioOnly:
		if prioritiseQuick {
			return domain.PriorityQuick
		}

		return domain.PriorityNormal
	case domain.KindSkip:
		return domain.PriorityNormal
	default:
		return domain.PriorityNormal
	}
}
