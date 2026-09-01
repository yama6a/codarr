package job

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/promote"
)

// task is one job's state as it walks the pipeline of plan.md 15.2.
type task struct {
	job       domain.Job
	media     domain.MediaFile
	settings  domain.Settings
	probe     *ffprobe.Result
	plan      domain.Plan
	transform domain.TransformRecord
	source    promote.SourceState
	staging   promote.Staging
	caps      hardware.Capabilities
	selection hardware.Selection

	// decodeRetried and forceSoftware carry the two fallback chains of 10.1 and
	// 10.2 across attempts: the software-decode retry is allowed once per
	// encoder, and re-armed when the encoder changes.
	decodeRetried bool
	forceSoftware bool

	needsIdet bool
	blocked   bool
	estimate  int
	duration  time.Duration
	finalOut  time.Duration
	decode    domain.DecodePath
	argv      []string
}

// execute runs one claimed job to a terminal state. Only three things end it:
// success, a cancel someone asked for, and a failure that always carries both
// halves of plan.md 19.1. A shutdown ends none of them, and deliberately leaves
// the row in flight for the sweep of 19.2.
func (s *Service) execute(parent context.Context, j domain.Job) error {
	s.observe(parent, domain.JobRunning, j.Kind, j.Origin)

	return s.withRunning(parent, j, s.pipeline)
}

// withRunning holds the one in-flight slot, and owns the three ways a job can
// leave it.
func (s *Service) withRunning(parent context.Context, j domain.Job, run func(ctx context.Context, j domain.Job) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	r := s.markRunning(j.ID, cancel)
	defer s.clearRunning(r)

	err := run(ctx, j)

	switch {
	case err == nil:
		return nil
	case s.cancelRequested(r):
		return s.finishCancelled(ctx, j, s.stagingOf(r))
	case ctx.Err() != nil:
		s.log.WarnContext(parent, "job interrupted by shutdown, the startup sweep will pick it up",
			slog.Int64("job_id", j.ID))

		return nil
	default:
		return s.finishFailed(ctx, j, classify(err), s.stagingOf(r))
	}
}

func (s *Service) stagingOf(r *running) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return r.stagingPath
}

func (s *Service) pipeline(ctx context.Context, j domain.Job) error {
	t, err := s.load(ctx, j)
	if err != nil {
		return err
	}

	if err := s.prepare(ctx, t); err != nil {
		return err
	}

	if err := s.encode(ctx, t); err != nil {
		return err
	}

	return s.finalise(ctx, t)
}

// load reads everything the job needs and re-plans from the stored probe. The
// plan is recomputed rather than trusted from the queue row because the policy
// is what decides, the probe is what it decides from, and only a fresh call
// reports whether an idet sample is still owed (6.2).
func (s *Service) load(ctx context.Context, j domain.Job) (*task, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, wrapf(domain.FailInternal, err, "the settings could not be read")
	}

	media, err := s.store.GetMediaFile(ctx, j.MediaFileID)
	if err != nil {
		return nil, wrapf(domain.FailInternal, err, "media file %d could not be read", j.MediaFileID)
	}

	probe, err := storedProbe(media)
	if err != nil {
		return nil, err
	}

	t := &task{job: j, media: media, settings: settings, probe: probe, duration: durationOf(probe)}

	if err := s.replan(t, ""); err != nil {
		return nil, err
	}

	t.transform = decide.NewTransform(probe, t.plan, 0)

	return t, nil
}

// replan applies the policy to the stored probe. scan is the answer to an
// earlier idet sample, empty when none has run.
func (s *Service) replan(t *task, scan domain.Scan) error {
	analysis, err := s.engine.Plan(t.probe, decide.Options{Path: t.media.Path, IdetScan: scan})
	if err != nil {
		return wrapf(domain.FailInternal, err, "planning %s against the current policy failed", t.media.Path)
	}

	plan := analysis.Plan

	// plan.md 11: the sweep is the only thing that re-encodes compliant video,
	// and origin is what carries that intent through the queue.
	if t.job.Origin == domain.OriginSpaceSweep {
		forced, ok := decide.ForceVideoEncode(plan, sweepReason)
		if !ok {
			return failf(domain.FailInternal,
				"%s cannot be re-encoded for the space sweep: the current policy plans its video as %s",
				t.media.Path, plan.Kind)
		}

		plan = forced
	}

	if !plan.NeedsWrite() {
		return failf(domain.FailInternal,
			"%s plans as skip under the current policy, so there is nothing to do; it was queued as %s under an older one",
			t.media.Path, t.job.Kind)
	}

	// The target survives a re-plan: the sample probe measures the content, not
	// the scan type, and re-running it would cost another three encodes.
	plan.TargetVideoBitrate = max(plan.TargetVideoBitrate, t.plan.TargetVideoBitrate)

	t.plan = plan
	t.needsIdet = analysis.NeedsIdetSample

	return nil
}

// prepare is everything between claiming the job and the first byte of output:
// preflight, the bitrate probe of 8.1, the interlacing sample of 6.2, the
// encoder choice of 10.2 and the estimate of 14.3.
func (s *Service) prepare(ctx context.Context, t *task) error {
	if err := s.preflight(t); err != nil {
		return err
	}

	if err := s.resolveBitrate(ctx, t); err != nil {
		return err
	}

	if err := s.resolveScan(ctx, t); err != nil {
		return err
	}

	if err := s.selectEncoder(ctx, t); err != nil {
		return err
	}

	t.estimate = s.est.Estimate(ctx, t.work())

	return s.writeTransform(ctx, t)
}

func (s *Service) preflight(t *task) error {
	t.source = promote.SourceState{
		SizeBytes:       t.media.SizeBytes,
		MTime:           t.media.MTime,
		Fingerprint:     t.media.Fingerprint,
		DurationSeconds: t.duration.Seconds(),
		Video:           t.transform.Video.Before,
		LegacyContainer: decide.IsLegacyContainer(t.plan.SourceContainer),
	}

	staging, err := s.promoter.Preflight(promote.PreflightRequest{
		JobID:      t.job.ID,
		SourcePath: t.media.Path,
		Source:     t.source,
		OutputExt:  t.plan.OutputContainer.OutputExt(t.media.Path),
	})
	if err != nil {
		return fmt.Errorf("preflight of %s: %w", t.media.Path, err)
	}

	t.staging = staging
	s.setStaging(staging.Path)

	return nil
}

// resolveBitrate is plan.md 8.1 run as the first phase of the job rather than
// at enqueue time, which is why 17.2 leaves after.video.bitrate_kbps null and
// the UI shows "calculating" until here. A failed probe is not a failed job:
// 8.2 is the fallback the formula exists for.
func (s *Service) resolveBitrate(ctx context.Context, t *task) error {
	if t.plan.Kind != domain.KindFull {
		return nil
	}

	in := bitrateInput(t.probe)

	base, err := s.sampleBase(ctx, t)
	if err != nil {
		if cancelled(ctx, err) {
			return err
		}

		s.mx.error(errorBitrateProb)
		s.log.WarnContext(ctx, "the bitrate sample probe failed, falling back to the 8.2 formula",
			slog.Int64("job_id", t.job.ID), slog.Any("error", err))

		t.plan.TargetVideoBitrate = ffmpeg.TargetFromFallback(in)

		return nil
	}

	t.plan.TargetVideoBitrate = ffmpeg.TargetFromSamples([]int{base}, in)

	s.log.InfoContext(ctx, "sample probe resolved the encode target",
		slog.Int64("job_id", t.job.ID),
		slog.Int("measured_bps", base),
		slog.Int("target_bps", t.plan.TargetVideoBitrate))

	return nil
}

func (s *Service) sampleBase(ctx context.Context, t *task) (int, error) {
	return s.sampleProbe(ctx, t.settings.TempDir, strconv.FormatInt(t.job.ID, 10), t.media.Path, t.duration.Seconds())
}

// sampleProbe is plan.md 8.1's three fixed-quality encodes. They go into a
// dotfile directory under the temp dir so the orphan sweep of 15.2 reclaims
// them if the process dies mid-probe.
func (s *Service) sampleProbe(ctx context.Context, tempDir, name, path string, durationSec float64) (int, error) {
	if tempDir == "" {
		return 0, failf(domain.FailProbe, "no temp directory is configured, so the sample probe has nowhere to write")
	}

	dir := filepath.Join(tempDir, ".codarr-probe-"+name)
	if err := s.fs.MkdirAll(dir, 0o755); err != nil {
		return 0, wrapf(domain.FailProbe, err, "the sample probe directory %s could not be created", dir)
	}

	defer func() {
		if err := s.fs.Remove(dir); err != nil {
			s.log.WarnContext(ctx, "removing the sample probe directory failed",
				slog.String("path", dir), slog.Any("error", err))
		}
	}()

	base, err := ffmpeg.NewSampleProbe(s.newEnc(0), s.fs, dir).Base(ctx, path, durationSec)
	if err != nil {
		return 0, wrapf(domain.FailProbe, err, "the sample probe of %s failed", path)
	}

	return base, nil
}

// resolveScan answers decide.Analysis.NeedsIdetSample. The engine refuses to
// shell out, so the short sample of 6.2 runs here and the file is re-planned
// with the answer.
func (s *Service) resolveScan(ctx context.Context, t *task) error {
	if !t.needsIdet {
		return nil
	}

	res, err := s.newEnc(0).Run(ctx, ffmpeg.IdetArgs(t.media.Path), nil)
	if err != nil {
		if cancelled(ctx, err) {
			return fmt.Errorf("idet sample of %s: %w", t.media.Path, err)
		}

		s.mx.error(errorIdet)
		s.log.WarnContext(ctx, "the idet sample failed, treating the source as progressive",
			slog.Int64("job_id", t.job.ID), slog.Any("error", err))

		return nil
	}

	scan := ffmpeg.ParseIdet(res.StderrTail)

	s.log.InfoContext(ctx, "idet sample decided the scan type",
		slog.Int64("job_id", t.job.ID), slog.String("scan", string(scan)))

	return s.replan(t, scan)
}

// selectEncoder is plan.md 10.2's preference order. The capability set is read
// for every job, not just the ones that encode: it also answers whether the
// source decodes on the iGPU, which the probe confirms per driver rather than
// per silicon (10.1).
func (s *Service) selectEncoder(ctx context.Context, t *task) error {
	caps, err := s.hw.Capabilities(ctx)
	if err != nil {
		return wrapf(domain.FailInternal, err, "the hardware capabilities could not be read for %s", t.media.Path)
	}

	t.caps = caps

	if t.plan.Kind != domain.KindFull {
		return nil
	}

	t.selection = caps.Select(t.plan.HDR)
	s.logSelection(ctx, t)

	return nil
}

func (s *Service) logSelection(ctx context.Context, t *task) {
	if !t.selection.FellBack {
		return
	}

	level := slog.LevelWarn
	if t.selection.Software {
		// plan.md 10.2: a silent software fallback turns a 20-minute job into a
		// four-hour one, so it is never merely informational.
		level = slog.LevelError
	}

	s.log.Log(ctx, level, "the preferred encoder is not being used",
		slog.Int64("job_id", t.job.ID),
		slog.String("encoder", string(t.selection.Encoder)),
		slog.Bool("software", t.selection.Software),
		slog.String("reason", t.selection.Reason))
}

// writeTransform is 17.2's "fill it in when the job starts": the record was
// written at enqueue with a null target bitrate, and by here the sample probe
// has one and the estimate has been refined with the encoder actually chosen.
func (s *Service) writeTransform(ctx context.Context, t *task) error {
	t.transform = decide.NewTransform(t.probe, t.plan, t.estimate)

	if t.plan.TargetVideoBitrate > 0 && t.transform.Video.After != nil {
		kbps := t.plan.TargetVideoBitrate / 1000
		t.transform.Video.After.BitrateKbps = &kbps
	}

	if err := s.store.UpdateJobTransform(ctx, t.job.ID, t.transform); err != nil {
		return wrapf(domain.FailInternal, err, "the transform record of job %d could not be updated", t.job.ID)
	}

	return nil
}

// encode runs the job, applying the two fallback chains of plan.md 10 in the
// order that costs least. The software-decode retry comes first and only once
// per encoder: it is one more attempt on the same silicon, while stepping the
// encoder chain can mean libx265 and four hours. Neither chain is sniffed out
// of ffmpeg's stderr, because driver error strings are not an API.
//
// These retries are not the domain.MaxAutoAttempts budget. That one counts
// process deaths (19.2); this one counts encoder combinations inside a single
// attempt.
func (s *Service) encode(ctx context.Context, t *task) error {
	var (
		tried  []string
		last   ffmpeg.RunResult
		lastEr error
	)

	for {
		cmd, err := s.build(t, t.forceSoftware)
		if err != nil {
			return err
		}

		res, runErr := s.attempt(ctx, t, cmd)
		if runErr == nil {
			// The final out_time is only known now, and 19.2 can resume this
			// job in another process, so it goes on the row rather than staying
			// in memory (14.3, 15.3).
			return s.recordExecution(ctx, t)
		}

		if cancelled(ctx, runErr) {
			return runErr
		}

		tried = append(tried, describeAttempt(t, cmd))
		last, lastEr = res, runErr

		if !s.stepBack(ctx, t, cmd, res) {
			return encodeExhausted(t, tried, last, lastEr)
		}
	}
}

// stepBack moves to the next thing worth trying and reports whether there was
// one. Changing encoder re-arms the decode retry, because a different backend
// fails differently.
func (s *Service) stepBack(ctx context.Context, t *task, cmd ffmpeg.Command, res ffmpeg.RunResult) bool {
	if retry, ok := hardware.RetryInSoftware(cmd.DecodePath, t.decodeRetried); ok {
		s.mx.decodeFallback()

		t.decodeRetried = true
		t.forceSoftware = true
		t.selection.FellBack = true
		t.selection.Reason = strings.TrimSpace(t.selection.Reason + " " + retry.Reason)

		s.log.WarnContext(ctx, "retrying the encode with software decode",
			slog.Int64("job_id", t.job.ID), slog.String("stderr", lastLine(res.StderrTail)))

		return true
	}

	if t.plan.Kind != domain.KindFull {
		return false
	}

	next, ok := t.caps.Next(t.selection.Encoder, t.plan.HDR)
	if !ok {
		return false
	}

	s.mx.encoderFallback(t.selection.Encoder, next.Encoder)

	t.selection = next
	t.decodeRetried = false
	t.forceSoftware = false

	s.logSelection(ctx, t)

	return true
}

// attempt records what is about to run and then runs it, so a crash mid-encode
// leaves the staging path and the argv on the row for 19.2 to act on.
func (s *Service) attempt(ctx context.Context, t *task, cmd ffmpeg.Command) (ffmpeg.RunResult, error) {
	t.decode = cmd.DecodePath
	t.argv = cmd.Args

	if err := s.recordExecution(ctx, t); err != nil {
		return ffmpeg.RunResult{}, err
	}

	res, err := s.runEncode(ctx, t, cmd.Args)
	if err != nil {
		return res, err
	}

	t.finalOut = res.FinalOutTime

	return res, nil
}

// runEncode wires ffmpeg's progress stream to the job row through the throttle
// of 14.3: the live value stays in memory and reaches SQLite every five
// seconds, never once per progress line.
func (s *Service) runEncode(ctx context.Context, t *task, args []string) (ffmpeg.RunResult, error) {
	writeCtx := context.WithoutCancel(ctx)

	throttle := ffmpeg.NewThrottle(s.clk, ffmpeg.FlushInterval, func(p ffmpeg.Progress) {
		if err := s.store.UpdateJobProgress(writeCtx, t.job.ID, p.Percent, p.Speed, p.FPS, t.estimate); err != nil {
			s.mx.error(errorProgress)
			s.log.WarnContext(writeCtx, "storing job progress failed",
				slog.Int64("job_id", t.job.ID), slog.Any("error", err))
		}
	})

	defer throttle.Flush()

	res, err := s.newEnc(t.duration).Run(ctx, args, throttle.Update)
	if err != nil {
		return res, fmt.Errorf("running ffmpeg for job %d: %w", t.job.ID, err)
	}

	return res, nil
}

func (s *Service) build(t *task, forceSoftwareDecode bool) (ffmpeg.Command, error) {
	video, _ := t.probe.PrimaryVideo()

	// 10.1: the hard-coded Gen 9.5 decode set is necessary but not sufficient.
	// VP9 decode is in the silicon and not always in the driver, so the probe
	// has the last word on whether -hwaccel is safe here.
	if t.caps.DecodePath(t.selection.Encoder, video.CodecName) == domain.DecodeSoftware {
		forceSoftwareDecode = true
	}

	cmd, err := ffmpeg.Build(ffmpeg.Request{
		Plan: t.plan,
		Source: ffmpeg.Source{
			Path:            t.media.Path,
			VideoCodec:      video.CodecName,
			LegacyContainer: decide.IsLegacyContainer(t.plan.SourceContainer),
		},
		Output:              t.staging.Path,
		Tags:                ffmpeg.Tags{Version: s.version, Policy: t.plan.PolicyHash},
		Encoder:             t.selection.Encoder,
		Device:              t.caps.Device,
		ForceSoftwareDecode: forceSoftwareDecode,
	})
	if err != nil {
		return ffmpeg.Command{}, wrapf(domain.FailInternal, err,
			"the ffmpeg invocation for %s could not be built", t.media.Path)
	}

	return cmd, nil
}

func (s *Service) recordExecution(ctx context.Context, t *task) error {
	err := s.store.UpdateJobExecution(ctx, store.ExecutionUpdate{
		JobID:            t.job.ID,
		StagingPath:      t.staging.Path,
		UsedTempDir:      t.staging.UsedTempDir,
		FfmpegArgv:       t.argv,
		EncoderUsed:      t.selection.Encoder,
		DecodePath:       t.decode,
		FellBack:         t.selection.FellBack,
		FallbackReason:   t.selection.Reason,
		SourceSize:       t.media.SizeBytes,
		EstimatedSeconds: t.estimate,
		FinalOutTimeUS:   t.finalOut.Microseconds(),
	})
	if err != nil {
		return wrapf(domain.FailInternal, err, "recording what job %d is running failed", t.job.ID)
	}

	return nil
}

// finalise is steps 3 to 10 of plan.md 15.2. The output is probed once here for
// the measured half of the transform record; the promoter probes it again as
// part of verification, which is the check that stands between a bad encode and
// a destroyed source and is never skipped.
func (s *Service) finalise(ctx context.Context, t *task) error {
	if err := s.setState(ctx, t, domain.JobVerifying); err != nil {
		return err
	}

	out, err := s.prober.Probe(ctx, t.staging.Path)
	if err != nil {
		return wrapf(domain.FailProbe, err, "the finished output %s could not be probed", t.staging.Path)
	}

	if err := s.setState(ctx, t, domain.JobPromoting); err != nil {
		return err
	}

	res, promoteErr := s.promoter.Promote(ctx, s.promoteRequest(ctx, t))

	// plan.md 15.2 step 7 has happened: the source inode is gone. The identity
	// has to be persisted even though the job failed, or provenance reads
	// untouched forever on a file Codarr wrote (12, and decisions.md).
	if promoteErr != nil && !res.Renamed {
		return fmt.Errorf("promoting %s: %w", t.media.Path, promoteErr)
	}

	if err := s.settle(ctx, t, out, res); err != nil {
		return err
	}

	if promoteErr != nil {
		return fmt.Errorf("promoting %s: %w", t.media.Path, promoteErr)
	}

	return nil
}

func (s *Service) promoteRequest(ctx context.Context, t *task) promote.Request {
	writeCtx := context.WithoutCancel(ctx)

	return promote.Request{
		JobID:               t.job.ID,
		SourcePath:          t.media.Path,
		Staging:             t.staging,
		Plan:                t.plan,
		Source:              t.source,
		FullHashEnabled:     t.settings.FullHashEnabled,
		FinalOutTimeSeconds: t.finalOut.Seconds(),
		OnBlocked: func(reason string) {
			t.blocked = true

			s.block(writeCtx, t, reason)
		},
	}
}

// block is plan.md 15.2 step 4: Plex is streaming the target, so the job waits
// rather than replacing a file an NFS client has open (15.6).
func (s *Service) block(ctx context.Context, t *task, reason string) {
	if err := s.store.SetJobState(ctx, t.job.ID, domain.JobAwaitingStreamEnd); err != nil {
		s.mx.error(errorState)
		s.log.WarnContext(ctx, "moving the job to awaiting_stream_end failed",
			slog.Int64("job_id", t.job.ID), slog.Any("error", err))
	}

	if err := s.store.SetJobBlockedBy(ctx, t.job.ID, reason); err != nil {
		s.mx.error(errorState)
		s.log.WarnContext(ctx, "recording what the job is blocked by failed",
			slog.Int64("job_id", t.job.ID), slog.Any("error", err))
	}

	s.observe(ctx, domain.JobAwaitingStreamEnd, t.plan.Kind, t.job.Origin)
}

// settle writes the measured half of the transform record and the output
// identity. It runs on a detached context: the rename already happened, so
// abandoning this write would lose the only record of what Codarr produced.
func (s *Service) settle(ctx context.Context, t *task, out *ffprobe.Result, res promote.Result) error {
	ctx = context.WithoutCancel(ctx)
	actual := s.elapsed(t.job)

	t.transform = decide.MergeMeasured(t.transform, out, actual)
	if res.Identity.Fingerprint != "" {
		identity := res.Identity
		t.transform.OutputIdentity = &identity
	}

	if err := s.recordPromotion(ctx, t, res, actual); err != nil {
		return err
	}

	if t.blocked {
		if err := s.store.SetJobBlockedBy(ctx, t.job.ID, ""); err != nil {
			s.mx.error(errorState)
			s.log.WarnContext(ctx, "clearing blocked_by failed",
				slog.Int64("job_id", t.job.ID), slog.Any("error", err))
		}
	}

	s.est.Record(ctx, t.work(), actual)
	s.recordDuration(t, actual)
	s.observe(ctx, domain.JobDone, t.plan.Kind, t.job.Origin)

	for _, w := range res.Warnings {
		s.log.WarnContext(ctx, "promotion warning", slog.Int64("job_id", t.job.ID), slog.String("warning", w))
	}

	s.log.InfoContext(ctx, "job promoted",
		slog.Int64("job_id", t.job.ID),
		slog.String("path", t.media.Path),
		slog.Int("estimated_seconds", t.estimate),
		slog.Int("actual_seconds", actual))

	return nil
}

func (s *Service) recordPromotion(ctx context.Context, t *task, res promote.Result, actual int) error {
	policyHash := res.Identity.PolicyHash
	if policyHash == "" {
		policyHash = t.plan.PolicyHash
	}

	err := s.store.RecordPromotion(ctx, store.PromotionUpdate{
		JobID:             t.job.ID,
		MediaFileID:       t.media.ID,
		OutputFingerprint: res.Identity.Fingerprint,
		OutputFullHash:    deref(res.Identity.FullHash),
		OutputSize:        res.OutputSize,
		OutputMTime:       res.Identity.MTime,
		PolicyHash:        policyHash,
		Transform:         t.transform,
		ActualSeconds:     actual,
		PromotedAt:        s.clk.Now(),
	})
	if err != nil {
		return wrapf(domain.FailInternal, err, "recording the promotion of job %d failed", t.job.ID)
	}

	return nil
}

func (s *Service) setState(ctx context.Context, t *task, state domain.JobState) error {
	if err := s.store.SetJobState(ctx, t.job.ID, state); err != nil {
		return wrapf(domain.FailInternal, err, "moving job %d to %s failed", t.job.ID, state)
	}

	s.observe(ctx, state, t.plan.Kind, t.job.Origin)

	return nil
}

func (s *Service) elapsed(j domain.Job) int {
	if j.StartedAt == nil {
		return 0
	}

	seconds := int(s.clk.Since(*j.StartedAt).Seconds())

	return max(seconds, 0)
}

func resolutionOf(probe *ffprobe.Result) string {
	video, ok := probe.PrimaryVideo()
	if !ok {
		return ""
	}

	return string(ffmpeg.ResolutionOf(video.Width, video.Height))
}

func (t *task) work() work {
	w := work{
		kind:         t.plan.Kind,
		encoder:      t.selection.Encoder,
		sourceBytes:  t.media.SizeBytes,
		mediaSeconds: t.duration.Seconds(),
	}

	w.resolution = resolutionOf(t.probe)

	return w
}

func storedProbe(m domain.MediaFile) (*ffprobe.Result, error) {
	if m.ProbeJSON == "" {
		return nil, failf(domain.FailProbe,
			"%s has no stored ffprobe result, so it cannot be planned; analyse it again first", m.Path)
	}

	probe, err := ffprobe.Parse([]byte(m.ProbeJSON))
	if err != nil {
		return nil, wrapf(domain.FailProbe, err, "the stored ffprobe result for %s could not be read", m.Path)
	}

	return probe, nil
}

func durationOf(probe *ffprobe.Result) time.Duration {
	return time.Duration(probe.Duration() * float64(time.Second))
}

func bitrateInput(probe *ffprobe.Result) ffmpeg.BitrateInput {
	video, _ := probe.PrimaryVideo()
	source, _ := decide.ResolveVideoBitrate(probe)

	return ffmpeg.BitrateInput{
		Width:         video.Width,
		Height:        video.Height,
		FPS:           video.FrameRate(),
		HDR:           video.IsHDR(),
		SourceBitrate: source,
	}
}

// describeAttempt names one encoder-and-decode-path combination, so an
// exhausted job says what was actually tried rather than "ffmpeg failed".
func describeAttempt(t *task, cmd ffmpeg.Command) string {
	encoder := string(t.selection.Encoder)
	if encoder == "" {
		encoder = "stream copy"
	}

	return encoder + " with " + string(cmd.DecodePath) + " decode"
}

// encodeExhausted is the failure after every combination has been tried. The
// runner's own error text repeats the whole stderr tail, so it is not folded
// into the message: the message stays readable and stderr_tail holds the rest
// (19.1).
func encodeExhausted(t *task, tried []string, res ffmpeg.RunResult, err error) *Error {
	reason := lastLine(res.StderrTail)
	if reason == "" {
		reason = err.Error()
	}

	f := failf(domain.FailFfmpeg, "ffmpeg did not write a usable output for %s after trying %s: %s",
		t.media.Path, strings.Join(tried, ", "), reason)
	f.StderrTail = res.StderrTail

	return f
}

// lastLine is the most recent non-empty stderr line, which is where ffmpeg puts
// the reason it gave up.
func lastLine(tail string) string {
	lines := strings.Split(tail, "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}

	return ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
