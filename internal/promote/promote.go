// Package promote is the irreversible half of Codarr: the rename destroys the
// original (15.5), so there is deliberately no way past a failed check.
package promote

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/promote_mock.go -pkg mock . Prober StreamGuard Fingerprinter Notifier Copier

// DefaultStreamRetry is how long the job waits before re-asking Plex whether the
// target is still being streamed (plan.md 15.2 step 4).
const DefaultStreamRetry = 60 * time.Second

// SpaceFactor is the free-space margin preflight demands, as a multiple of the
// source size (plan.md 15.4).
const SpaceFactor = 1.2

// Prober is ffprobe, narrowed to what verification asks of it.
type Prober interface {
	Probe(ctx context.Context, path string) (Output, error)
}

// StreamGuard answers whether Plex is streaming a path; replacing a file an NFS
// client has open gives that client ESTALE (plan.md 15.6).
type StreamGuard interface {
	IsStreaming(ctx context.Context, path string) (bool, string, error)
}

// Fingerprinter is the file identity of plan.md 12.1 and 12.2.
type Fingerprinter interface {
	Sparse(path string) (string, error)
	Full(path string) (string, error)
}

// Notifier is the post-promotion refresh of Plex and the owning *arr, running
// after the source is gone, so a failure is only a warning.
type Notifier interface {
	NotifyPromoted(ctx context.Context, path string) error
}

// Copier moves the staging file onto the destination filesystem when it landed
// on another device.
type Copier interface {
	Copy(ctx context.Context, src, dst string) (int64, error)
}

// FSCopier is the Copier every caller should use, because fsx.Copy fsyncs the
// destination before returning.
type FSCopier struct {
	fs fsx.FS
}

var _ Copier = (*FSCopier)(nil)

// NewFSCopier returns a Copier backed by fs.
func NewFSCopier(fs fsx.FS) *FSCopier { return &FSCopier{fs: fs} }

func (c *FSCopier) Copy(ctx context.Context, src, dst string) (int64, error) {
	n, err := c.fs.Copy(ctx, src, dst)
	if err != nil {
		return n, fmt.Errorf("copy staged output: %w", err)
	}

	return n, nil
}

// Deps is everything a Promoter needs. Nothing is instantiated internally.
type Deps struct {
	FS            fsx.FS
	Clock         clock.Clock
	Prober        Prober
	Guard         StreamGuard
	Fingerprinter Fingerprinter
	Notifier      Notifier
	Copier        Copier
	Logger        *slog.Logger
	TempDir       string
	StreamRetry   time.Duration

	// Metrics is optional. A nil value records nothing and is safe everywhere.
	Metrics Metrics
}

// Promoter runs preflight, verification and the atomic replace.
type Promoter struct {
	fs          fsx.FS
	clk         clock.Clock
	prober      Prober
	guard       StreamGuard
	fp          Fingerprinter
	notifier    Notifier
	copier      Copier
	log         *slog.Logger
	mx          recorder
	tempDir     string
	streamRetry time.Duration
}

// New returns a Promoter.
func New(d Deps) *Promoter {
	if d.StreamRetry <= 0 {
		d.StreamRetry = DefaultStreamRetry
	}

	if d.Logger == nil {
		d.Logger = slog.Default()
	}

	return &Promoter{
		fs:          d.FS,
		clk:         d.Clock,
		prober:      d.Prober,
		guard:       d.Guard,
		fp:          d.Fingerprinter,
		notifier:    d.Notifier,
		copier:      d.Copier,
		log:         d.Logger,
		mx:          recorder{m: d.Metrics},
		tempDir:     d.TempDir,
		streamRetry: d.StreamRetry,
	}
}

// Request is one promotion, steps 3 to 10 of plan.md 15.2, with the encode
// itself happening between Preflight and here.
type Request struct {
	JobID           int64
	SourcePath      string
	Staging         Staging
	Plan            domain.Plan
	Source          SourceState
	FullHashEnabled bool

	// FinalOutTimeSeconds is ffmpeg's own last out_time (plan.md 14.3), used as
	// ground truth when a legacy container lies about the source duration.
	FinalOutTimeSeconds float64

	// OnBlocked is called each time Plex reports the target is being streamed,
	// so the caller can move the job to awaiting_stream_end. Optional.
	OnBlocked func(reason string)
}

// Result is what promotion produced, returned on the error paths too, as far as
// promotion got.
type Result struct {
	Identity   domain.OutputIdentity
	OutputSize int64
	Warnings   []string

	// Renamed reports that step 7 of plan.md 15.2 completed. When true the caller
	// MUST persist Identity even on error, or provenance reads "untouched" forever (12).
	Renamed bool
}

// Promote verifies the staging file and replaces the source with it; every return
// path before the rename leaves the source untouched.
func (p *Promoter) Promote(ctx context.Context, req Request) (Result, error) {
	warnings, err := p.Verify(ctx, req)
	if err != nil {
		return Result{Warnings: warnings}, err
	}

	fullHash, err := p.fullHash(req)
	if err != nil {
		return Result{Warnings: warnings}, err
	}

	origin, err := p.originState(req.SourcePath)
	if err != nil {
		return Result{Warnings: warnings}, err
	}

	staging, err := p.stageOnDestination(ctx, req)
	if err != nil {
		return Result{Warnings: warnings}, err
	}

	if err := p.replace(ctx, req, staging); err != nil {
		return Result{Warnings: warnings}, err
	}

	return p.settle(ctx, req, fullHash, origin, warnings)
}

// The source is already gone here, so no path may abandon the output identity:
// every one returns a Result with Renamed set.
func (p *Promoter) settle(
	ctx context.Context,
	req Request,
	fullHash *string,
	origin fsx.FileInfo,
	warnings []string,
) (Result, error) {
	restoreWarnings, restoreErr := p.restore(ctx, req.SourcePath, origin)
	warnings = append(warnings, restoreWarnings...)

	// plan.md 15.2 step 9: after the metadata restore, because restoring mtime
	// changes what a later scan compares against.
	identity, identityErr := p.recordIdentity(req, fullHash)

	result := Result{Identity: identity, OutputSize: identity.SizeBytes, Warnings: warnings, Renamed: true}

	if restoreErr != nil {
		return result, restoreErr
	}

	if identityErr != nil {
		return result, identityErr
	}

	if err := p.notifier.NotifyPromoted(ctx, req.SourcePath); err != nil {
		p.mx.error(ErrorNotify)
		result.Warnings = append(result.Warnings,
			"the file was promoted but notifying Plex and the *arr failed: "+err.Error())
		p.log.WarnContext(ctx, "post-promotion notification failed",
			slog.Int64("job_id", req.JobID), slog.String("path", req.SourcePath), slog.Any("error", err))
	}

	return result, nil
}

func (p *Promoter) fullHash(req Request) (*string, error) {
	if !req.FullHashEnabled {
		return nil, nil //nolint:nilnil // absent is the normal case; the column is nullable
	}

	// plan.md 12.2: over the staging file, whose bytes the rename does not change,
	// so this is the value the promoted file has.
	sum, err := p.fp.Full(req.Staging.Path)
	if err != nil {
		return nil, wrap(domain.FailPromote, err, "computing the whole-file hash of %s failed", req.Staging.Path)
	}

	return &sum, nil
}

// Preflight ran before an encode that may have taken hours, and the nlink guard
// of plan.md 15.4 only counts if it holds at the moment of the rename.
func (p *Promoter) originState(sourcePath string) (fsx.FileInfo, error) {
	info, err := p.fs.Stat(sourcePath)
	if err != nil {
		return fsx.FileInfo{}, wrap(domain.FailPromote, err, "the source %s could not be stat'd before the replace", sourcePath)
	}

	if info.NLink != 1 {
		return fsx.FileInfo{}, fail(domain.FailPromote,
			"the source %s gained hard links during the encode (nlink %d); replacing it would damage the other copies",
			sourcePath, info.NLink)
	}

	return info, nil
}

// plan.md 15.1: rename(2) is not atomic across filesystems, so a staging file on
// another device is copied to a destination-side sibling first.
func (p *Promoter) stageOnDestination(ctx context.Context, req Request) (string, error) {
	if !req.Staging.CrossDevice {
		return req.Staging.Path, nil
	}

	if err := p.checkRoomForCopy(req); err != nil {
		return "", err
	}

	if _, err := p.copier.Copy(ctx, req.Staging.Path, req.Staging.FinalPath); err != nil {
		return "", wrap(domain.FailPromote, err,
			"copying the staged output from %s onto the destination filesystem at %s failed",
			req.Staging.Path, req.Staging.FinalPath)
	}

	return req.Staging.FinalPath, nil
}

// Preflight gated on 1.2x the SOURCE size, but what is copied back is the OUTPUT,
// and running out of space mid-copy leaves a partial dotfile (plan.md 15.1).
func (p *Promoter) checkRoomForCopy(req Request) error {
	info, err := p.fs.Stat(req.Staging.Path)
	if err != nil {
		return wrap(domain.FailPreflight, err, "the staged output %s could not be stat'd", req.Staging.Path)
	}

	destDir := filepath.Dir(req.Staging.FinalPath)

	space, err := p.fs.Statfs(destDir)
	if err != nil {
		return wrap(domain.FailPreflight, err, "free space on %s could not be read", destDir)
	}

	if need := uint64(info.Size); space.FreeBytes < need {
		return fail(domain.FailPreflight,
			"the destination %s has %d bytes free, which is not enough for the %d byte staged output",
			destDir, space.FreeBytes, need)
	}

	return nil
}

func (p *Promoter) replace(ctx context.Context, req Request, staging string) error {
	destDir := filepath.Dir(req.SourcePath)

	for {
		if err := p.waitForStreamEnd(ctx, req); err != nil {
			return err
		}

		if err := p.sync(staging, destDir); err != nil {
			return err
		}

		blocked, reason, err := p.recheckAndRename(ctx, staging, req.SourcePath)
		if err != nil {
			return wrap(domain.FailPromote, err, "replacing %s with the staged output failed", req.SourcePath)
		}

		if !blocked {
			return nil
		}

		// The final check changed its mind; nothing was renamed.
		if err := p.deferReplace(ctx, req, reason); err != nil {
			return err
		}
	}
}

func (p *Promoter) waitForStreamEnd(ctx context.Context, req Request) error {
	for {
		blocked, reason := p.blocked(ctx, req.SourcePath)
		if !blocked {
			return nil
		}

		if err := p.deferReplace(ctx, req, reason); err != nil {
			return err
		}
	}
}

// plan.md 15.6 makes an unanswerable Plex as unsafe as a streaming one, and
// deferring costs nothing where failing would throw away a finished encode.
func (p *Promoter) blocked(ctx context.Context, path string) (bool, string) {
	streaming, who, err := p.guard.IsStreaming(ctx, path)

	switch {
	case err != nil:
		p.mx.error(ErrorStreamGuard)

		return true, "Plex could not be asked whether the file is streaming: " + err.Error()
	case streaming:
		return true, who
	default:
		return false, ""
	}
}

// deferReplace reports the block and waits out one retry interval.
func (p *Promoter) deferReplace(ctx context.Context, req Request, reason string) error {
	if req.OnBlocked != nil {
		req.OnBlocked(reason)
	}

	p.log.InfoContext(ctx, "the replace is blocked, waiting",
		slog.Int64("job_id", req.JobID), slog.String("path", req.SourcePath), slog.String("reason", reason))

	select {
	case <-ctx.Done():
		return wrap(domain.FailPromote, ctx.Err(), "waiting to replace %s was cancelled", req.SourcePath)
	case <-p.clk.After(p.streamRetry):
		return nil
	}
}

// plan.md 15.2 step 5: the data and the directory entry are separately durable,
// so both are flushed before the rename.
func (p *Promoter) sync(staging, destDir string) error {
	if err := p.fs.SyncFile(staging); err != nil {
		return wrap(domain.FailPromote, err, "fsync of the staging file %s failed", staging)
	}

	if err := p.fs.SyncDir(destDir); err != nil {
		return wrap(domain.FailPromote, err, "fsync of the destination directory %s failed", destDir)
	}

	return nil
}

// Nothing may be logged, written to the database or allocated between the Plex
// check and the rename (plan.md 15.6): a stream starting in that gap dies with ESTALE.
func (p *Promoter) recheckAndRename(ctx context.Context, staging, dest string) (bool, string, error) {
	streaming, who, err := p.guard.IsStreaming(ctx, dest)
	if err != nil {
		p.mx.error(ErrorStreamGuard)

		//nolint:nilerr // deliberate: an unanswerable Plex is a deferral, not a failure
		return true, "Plex could not be asked whether the file is streaming: " + err.Error(), nil
	}

	if streaming {
		return true, who, nil
	}

	return false, "", p.fs.Rename(staging, dest) //nolint:wrapcheck // wrapped by the caller; nothing may allocate here
}

// plan.md 15.2 step 8: a chown failure is expected under root_squash and is never
// a job failure, while mode and mtime need no privilege and must succeed.
func (p *Promoter) restore(ctx context.Context, dest string, origin fsx.FileInfo) ([]string, error) {
	var warnings []string

	if err := p.fs.Chown(dest, origin.UID, origin.GID); err != nil {
		warnings = append(warnings,
			fmtf("ownership %d:%d was not restored on %s: %v (expected under root_squash)", origin.UID, origin.GID, dest, err))
		p.log.WarnContext(ctx, "restoring ownership failed, continuing",
			slog.String("path", dest), slog.Int("uid", origin.UID), slog.Int("gid", origin.GID), slog.Any("error", err))
	}

	// Both are attempted even when the first fails, so the promoted file lands as
	// close to the original as the export allows.
	var failure error

	if err := p.fs.Chmod(dest, origin.Mode.Perm()); err != nil {
		failure = wrap(domain.FailPromote, err,
			"the replace succeeded but restoring mode %o on %s failed", origin.Mode.Perm(), dest)
	}

	if err := p.fs.Chtimes(dest, origin.MTime, origin.MTime); err != nil && failure == nil {
		failure = wrap(domain.FailPromote, err,
			"the replace succeeded but restoring the modification time on %s failed", dest)
	}

	return warnings, failure
}

func (p *Promoter) recordIdentity(req Request, fullHash *string) (domain.OutputIdentity, error) {
	info, err := p.fs.Stat(req.SourcePath)
	if err != nil {
		return domain.OutputIdentity{}, wrap(domain.FailPromote, err,
			"the replace succeeded but the promoted file %s could not be stat'd, so no output identity was recorded", req.SourcePath)
	}

	sum, err := p.fp.Sparse(req.SourcePath)
	if err != nil {
		return domain.OutputIdentity{}, wrap(domain.FailPromote, err,
			"the replace succeeded but fingerprinting the promoted file %s failed, so it would be re-encoded on the next scan", req.SourcePath)
	}

	return domain.OutputIdentity{
		Fingerprint: sum,
		FullHash:    fullHash,
		SizeBytes:   info.Size,
		MTime:       info.MTime.Unix(),
		PolicyHash:  req.Plan.PolicyHash,
		RecordedAt:  p.clk.Now(),
	}, nil
}
