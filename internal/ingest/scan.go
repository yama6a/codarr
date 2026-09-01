package ingest

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// ScanReport is what one pass did. Every file is accounted for in exactly one
// counter, which is what makes "the numbers do not add up" a usable bug report.
type ScanReport struct {
	Roots      int
	Walked     int
	Analyzed   int
	Queued     int
	Unchanged  int
	Excluded   int
	Unstable   int
	Missing    int
	Failed     int
	StartedAt  time.Time
	FinishedAt time.Time
}

// Scanner is the daily safety net of plan.md 13.2.
type Scanner struct {
	fs       FS
	store    ScanStore
	analyzer FileAnalyzer
	clock    clock.Clock
	logger   *slog.Logger
}

// NewScanner returns a Scanner.
func NewScanner(fs FS, st ScanStore, analyzer FileAnalyzer, clk clock.Clock, logger *slog.Logger) *Scanner {
	return &Scanner{
		fs:       fs,
		store:    st,
		analyzer: analyzer,
		clock:    clk,
		logger:   logger.With(slog.String("component", "ingest.scan")),
	}
}

// ScanAll walks every enabled root. This is what the schedule fires and what
// the manual trigger calls.
func (s *Scanner) ScanAll(ctx context.Context) (ScanReport, error) {
	env, roots, err := s.env(ctx)
	if err != nil {
		return ScanReport{}, err
	}

	report := ScanReport{StartedAt: s.clock.Now()}
	lim := newLimiter(s.clock, env.Settings.ScanRateLimitFPS)

	for _, root := range roots {
		if !root.Enabled {
			continue
		}

		report.Roots++

		if err := s.scanRoot(ctx, root, env, lim, &report); err != nil {
			if ctx.Err() != nil {
				return s.finish(report), fmt.Errorf("scan cancelled: %w", err)
			}

			s.logger.Error("root skipped",
				slog.String("path", root.Path), slog.String("error", err.Error()))
		}
	}

	return s.finish(report), nil
}

// ScanRoot is the manual per-root trigger behind POST /api/roots/{id}/scan.
func (s *Scanner) ScanRoot(ctx context.Context, rootID int64) (ScanReport, error) {
	env, _, err := s.env(ctx)
	if err != nil {
		return ScanReport{}, err
	}

	root, err := s.store.GetRoot(ctx, rootID)
	if err != nil {
		return ScanReport{}, fmt.Errorf("get root %d: %w", rootID, err)
	}

	report := ScanReport{StartedAt: s.clock.Now(), Roots: 1}
	lim := newLimiter(s.clock, env.Settings.ScanRateLimitFPS)

	if err := s.scanRoot(ctx, root, env, lim, &report); err != nil {
		return s.finish(report), err
	}

	return s.finish(report), nil
}

func (s *Scanner) env(ctx context.Context) (Env, []domain.Root, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return Env{}, nil, fmt.Errorf("get settings: %w", err)
	}

	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		return Env{}, nil, fmt.Errorf("list roots: %w", err)
	}

	return Env{Roots: roots, Settings: settings, Origin: domain.OriginIngest}, roots, nil
}

func (s *Scanner) finish(r ScanReport) ScanReport {
	r.FinishedAt = s.clock.Now()

	s.logger.Info("scan complete",
		slog.Int("roots", r.Roots), slog.Int("walked", r.Walked),
		slog.Int("analyzed", r.Analyzed), slog.Int("queued", r.Queued),
		slog.Int("unchanged", r.Unchanged), slog.Int("excluded", r.Excluded),
		slog.Int("unstable", r.Unstable), slog.Int("missing", r.Missing),
		slog.Int("failed", r.Failed))

	return r
}

func (s *Scanner) scanRoot(ctx context.Context, root domain.Root, env Env,
	lim *limiter, report *ScanReport,
) error {
	// An unmounted NFS export walks as an empty tree, which would mark the whole
	// library missing, so the root is checked before anything is pruned (13.2).
	info, err := s.fs.Stat(root.Path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrRootUnreadable, root.Path, err)
	}

	if !info.IsDir {
		return fmt.Errorf("%w: %s is not a directory", ErrRootUnreadable, root.Path)
	}

	known, err := s.store.ListMediaStatsByRoot(ctx, root.ID)
	if err != nil {
		return fmt.Errorf("list known files under %s: %w", root.Path, err)
	}

	byPath := make(map[string]store.MediaStat, len(known))
	for _, k := range known {
		byPath[k.Path] = k
	}

	seen := make(map[string]struct{}, len(known))

	if err := s.walk(ctx, root, env, lim, report, byPath, seen); err != nil {
		return err
	}

	return s.prune(ctx, known, seen, report)
}

func (s *Scanner) walk(ctx context.Context, root domain.Root, env Env, lim *limiter,
	report *ScanReport, byPath map[string]store.MediaStat, seen map[string]struct{},
) error {
	err := s.fs.WalkDir(root.Path, func(path string, info fsx.FileInfo, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if walkErr != nil {
			if path == root.Path {
				return walkErr
			}

			s.logger.Warn("walk error, continuing",
				slog.String("path", path), slog.String("error", walkErr.Error()))

			return nil
		}

		if info.IsDir {
			if path != root.Path && ExcludeDir(filepath.Base(path)) {
				return fs.SkipDir
			}

			return nil
		}

		// Recorded before exclusion so a row for an excluded or ignored file is
		// never pruned as missing.
		seen[path] = struct{}{}
		report.Walked++

		return s.visit(ctx, path, info, env, lim, report, byPath)
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", root.Path, err)
	}

	return nil
}

func (s *Scanner) visit(ctx context.Context, path string, info fsx.FileInfo, env Env,
	lim *limiter, report *ScanReport, byPath map[string]store.MediaStat,
) error {
	if ExcludeFile(path, info.Size) != NotExcluded {
		report.Excluded++

		return nil
	}

	// plan.md 13.2: a file written in the last two minutes may still be being
	// written. One stat field, and it prevents transcoding half a copy.
	if s.clock.Now().Sub(info.MTime) < StabilityWindow {
		report.Unstable++

		return nil
	}

	if prior, ok := byPath[path]; ok && unchanged(prior, info) {
		report.Unchanged++

		return nil
	}

	if err := lim.wait(ctx); err != nil {
		return err
	}

	res, err := s.analyzer.AnalyzeIn(ctx, path, env)
	if err != nil {
		report.Failed++

		s.logger.Error("analysis failed",
			slog.String("path", path), slog.String("error", err.Error()))

		return nil
	}

	if res.Excluded != NotExcluded {
		report.Excluded++

		return nil
	}

	report.Analyzed++

	if res.Queued {
		report.Queued++
	}

	return nil
}

// Same size and same mtime means no re-analysis (plan.md 12). A row still marked new
// or missing is re-analysed whatever the stat says.
func unchanged(prior store.MediaStat, info fsx.FileInfo) bool {
	if prior.Status == domain.MediaNew || prior.Status == domain.MediaMissing {
		return false
	}

	return prior.SizeBytes == info.Size && prior.MTime == info.MTime.Unix()
}

// Rows whose path is gone go missing rather than away: if the path comes back, the next
// scan re-fingerprints and re-analyses it (13.2).
func (s *Scanner) prune(ctx context.Context, known []store.MediaStat,
	seen map[string]struct{}, report *ScanReport,
) error {
	var gone []int64

	for _, k := range known {
		if _, ok := seen[k.Path]; ok || k.Status == domain.MediaMissing {
			continue
		}

		// The walk prunes extras directories, so absence from it is not proof.
		// One stat per candidate settles it.
		if _, err := s.fs.Stat(k.Path); err == nil {
			continue
		}

		gone = append(gone, k.ID)
	}

	if len(gone) == 0 {
		return nil
	}

	n, err := s.store.MarkMediaMissing(ctx, gone)
	if err != nil {
		return fmt.Errorf("mark %d files missing: %w", len(gone), err)
	}

	report.Missing += int(n)

	return nil
}

// limiter paces analysis so a scan does not thrash the array (13.2). Zero or a
// negative rate means no limit.
type limiter struct {
	clock    clock.Clock
	interval time.Duration
	last     time.Time
}

func newLimiter(clk clock.Clock, filesPerSecond int) *limiter {
	l := &limiter{clock: clk}
	if filesPerSecond > 0 {
		l.interval = time.Second / time.Duration(filesPerSecond)
	}

	return l
}

func (l *limiter) wait(ctx context.Context) error {
	now := l.clock.Now()

	if l.interval > 0 && !l.last.IsZero() {
		if d := l.interval - now.Sub(l.last); d > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("rate limit wait: %w", ctx.Err())
			case <-l.clock.After(d):
			}

			now = l.clock.Now()
		}
	}

	l.last = now

	return nil
}
