// Package promote is the irreversible half of Codarr. plan.md 15.5: the rename
// destroys the original, there is no trash and no undo, so verification (15.3)
// and preflight (15.4) are the only things between a bad encode and a lost
// source. There is deliberately no way to promote past a failed check.
package promote

import (
	"context"
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

// StreamGuard answers whether Plex is currently streaming a path. plan.md 15.6:
// replacing a file an NFS client has open gives that client ESTALE, so this is
// load-bearing rather than defensive. The string is a human-readable
// description of the session, for the job's blocked_by column.
type StreamGuard interface {
	IsStreaming(ctx context.Context, path string) (bool, string, error)
}

// Fingerprinter is the file identity of plan.md 12.1 and 12.2.
type Fingerprinter interface {
	Sparse(path string) (string, error)
	Full(path string) (string, error)
}

// Notifier is the post-promotion refresh of Plex and the owning *arr instance.
// It runs after the source is already gone, so a failure is a warning.
type Notifier interface {
	NotifyPromoted(ctx context.Context, path string) error
}

// Copier moves the staging file onto the destination filesystem when the temp
// fallback was used. fsx.FS is a read, stat and rename boundary and has no
// write primitive, so this is its own seam.
type Copier interface {
	Copy(ctx context.Context, src, dst string) (int64, error)
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
		tempDir:     d.TempDir,
		streamRetry: d.StreamRetry,
	}
}

// Request is one promotion: steps 3 to 10 of plan.md 15.2. The encode itself
// happens between Preflight and here.
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

// Result is what promotion produced.
type Result struct {
	Identity   domain.OutputIdentity
	OutputSize int64
	Warnings   []string
}

// Promote verifies the staging file and replaces the source with it. Every
// return path before the rename leaves the source untouched and the staging
// file in place for inspection.
func (p *Promoter) Promote(ctx context.Context, req Request) (Result, error) {
	warnings, err := p.Verify(ctx, req)
	if err != nil {
		return Result{}, err
	}

	fullHash, err := p.fullHash(req)
	if err != nil {
		return Result{}, err
	}

	origin, err := p.originState(req.SourcePath)
	if err != nil {
		return Result{}, err
	}

	staging, err := p.stageOnDestination(ctx, req)
	if err != nil {
		return Result{}, err
	}

	if err := p.replace(ctx, req, staging); err != nil {
		return Result{}, err
	}

	restoreWarnings, err := p.restore(ctx, req.SourcePath, origin)

	warnings = append(warnings, restoreWarnings...)

	if err != nil {
		return Result{}, err
	}

	// plan.md 15.2 step 9: after the metadata restore, because restoring mtime
	// changes what a later scan compares against.
	identity, err := p.recordIdentity(req, fullHash)
	if err != nil {
		return Result{}, err
	}

	if err := p.notifier.NotifyPromoted(ctx, req.SourcePath); err != nil {
		warnings = append(warnings, "the file was promoted but notifying Plex and the *arr failed: "+err.Error())
		p.log.WarnContext(ctx, "post-promotion notification failed",
			slog.Int64("job_id", req.JobID), slog.String("path", req.SourcePath), slog.Any("error", err))
	}

	return Result{Identity: identity, OutputSize: identity.SizeBytes, Warnings: warnings}, nil
}

func (p *Promoter) fullHash(req Request) (*string, error) {
	if !req.FullHashEnabled {
		return nil, nil //nolint:nilnil // absent is the normal case; the column is nullable
	}

	// plan.md 12.2: over the staging file, after verification and before
	// promotion. The bytes do not change in the rename, so this is the same
	// value the promoted file has.
	sum, err := p.fp.Full(req.Staging.Path)
	if err != nil {
		return nil, wrap(domain.FailPromote, err, "computing the whole-file hash of %s failed", req.Staging.Path)
	}

	return &sum, nil
}

// originState re-stats the source immediately before the replace. Preflight ran
// before an encode that may have taken hours, and the nlink guard of plan.md
// 15.4 is only worth anything if it is true at the moment of the rename.
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

// stageOnDestination returns the path the rename will take its argument from.
// plan.md 15.1: rename(2) is not atomic across filesystems, so a staging file
// on another device is copied to a destination-side sibling first.
func (p *Promoter) stageOnDestination(ctx context.Context, req Request) (string, error) {
	if !req.Staging.CrossDevice {
		return req.Staging.Path, nil
	}

	if _, err := p.copier.Copy(ctx, req.Staging.Path, req.Staging.FinalPath); err != nil {
		return "", wrap(domain.FailPromote, err,
			"copying the staged output from %s onto the destination filesystem at %s failed",
			req.Staging.Path, req.Staging.FinalPath)
	}

	return req.Staging.FinalPath, nil
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

		streaming, err := p.recheckAndRename(ctx, staging, req.SourcePath)
		if err != nil {
			return wrap(domain.FailPromote, err, "replacing %s with the staged output failed", req.SourcePath)
		}

		if !streaming {
			return nil
		}

		// A stream started inside the re-check window. Nothing was renamed;
		// go back to waiting.
		p.log.InfoContext(ctx, "a stream started during the final check, deferring the replace",
			slog.Int64("job_id", req.JobID), slog.String("path", req.SourcePath))
	}
}

func (p *Promoter) waitForStreamEnd(ctx context.Context, req Request) error {
	for {
		streaming, who, err := p.guard.IsStreaming(ctx, req.SourcePath)
		if err != nil {
			// Fail closed. plan.md 15.6: an unknown answer is not a safe answer,
			// because replacing a streamed file gives the reader ESTALE.
			return wrap(domain.FailPromote, err,
				"could not determine whether Plex is streaming %s, so the replace was not attempted", req.SourcePath)
		}

		if !streaming {
			return nil
		}

		if req.OnBlocked != nil {
			req.OnBlocked(who)
		}

		p.log.InfoContext(ctx, "target is being streamed, waiting",
			slog.Int64("job_id", req.JobID), slog.String("path", req.SourcePath), slog.String("session", who))

		select {
		case <-ctx.Done():
			return wrap(domain.FailPromote, ctx.Err(), "waiting for the stream on %s to end was cancelled", req.SourcePath)
		case <-p.clk.After(p.streamRetry):
		}
	}
}

// sync is plan.md 15.2 step 5: the data and the directory entry are separately
// durable, so both are flushed before the rename.
func (p *Promoter) sync(staging, destDir string) error {
	if err := p.fs.SyncFile(staging); err != nil {
		return wrap(domain.FailPromote, err, "fsync of the staging file %s failed", staging)
	}

	if err := p.fs.SyncDir(destDir); err != nil {
		return wrap(domain.FailPromote, err, "fsync of the destination directory %s failed", destDir)
	}

	return nil
}

// recheckAndRename is three statements on purpose. plan.md 15.6 requires no
// logging, no database write and no allocation between the final Plex check and
// the rename: a stream starting in that window dies with ESTALE rather than
// degrading. Everything else has already happened by the time this is called.
func (p *Promoter) recheckAndRename(ctx context.Context, staging, dest string) (bool, error) {
	streaming, _, err := p.guard.IsStreaming(ctx, dest)
	if streaming || err != nil {
		return streaming, err
	}

	return false, p.fs.Rename(staging, dest)
}

// restore is plan.md 15.2 step 8. A chown failure is expected under root_squash
// and is never a job failure; mode and mtime need no privilege and must succeed.
func (p *Promoter) restore(ctx context.Context, dest string, origin fsx.FileInfo) ([]string, error) {
	var warnings []string

	if err := p.fs.Chown(dest, origin.UID, origin.GID); err != nil {
		warnings = append(warnings,
			fmtf("ownership %d:%d was not restored on %s: %v (expected under root_squash)", origin.UID, origin.GID, dest, err))
		p.log.WarnContext(ctx, "restoring ownership failed, continuing",
			slog.String("path", dest), slog.Int("uid", origin.UID), slog.Int("gid", origin.GID), slog.Any("error", err))
	}

	if err := p.fs.Chmod(dest, origin.Mode.Perm()); err != nil {
		return warnings, wrap(domain.FailPromote, err,
			"the replace succeeded but restoring mode %o on %s failed", origin.Mode.Perm(), dest)
	}

	if err := p.fs.Chtimes(dest, origin.MTime, origin.MTime); err != nil {
		return warnings, wrap(domain.FailPromote, err,
			"the replace succeeded but restoring the modification time on %s failed", dest)
	}

	return warnings, nil
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
