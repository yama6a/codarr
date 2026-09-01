package promote

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/fsx"
)

// Sweep removes staging files and writability probes no live job claims, plus
// whatever the temp directory still holds. plan.md 15.2: it runs at startup,
// after the job-state sweep of 19.2 has claimed what it can, so anything left
// unclaimed is debris from a crash.
//
// It returns everything it removed. A failure on one entry is logged and the
// sweep continues; the returned error joins them, so a caller can log it and
// carry on.
func (p *Promoter) Sweep(ctx context.Context, roots, claimed []string) ([]string, error) {
	held := map[string]bool{}

	for _, c := range claimed {
		if c != "" {
			held[filepath.Clean(c)] = true
		}
	}

	s := &sweeper{promoter: p, ctx: ctx, held: held}

	for _, root := range roots {
		s.walk(root)
	}

	s.temp()
	sort.Strings(s.removed)

	if len(s.errs) > 0 {
		p.mx.error(ErrorOrphanSweep)
	}

	return s.removed, errors.Join(s.errs...)
}

type sweeper struct {
	promoter *Promoter
	ctx      context.Context //nolint:containedctx // one sweep, one context; the alternative is threading it through every helper
	held     map[string]bool
	removed  []string
	errs     []error
}

func (s *sweeper) walk(root string) {
	err := s.promoter.fs.WalkDir(root, func(path string, _ fsx.FileInfo, err error) error {
		switch {
		case err != nil:
			s.errs = append(s.errs, err)
		case isDebris(filepath.Base(path)):
			s.remove(path)
		}

		return nil
	})
	if err != nil {
		s.errs = append(s.errs, err)
	}
}

func (s *sweeper) temp() {
	if s.promoter.tempDir == "" {
		return
	}

	matches, err := s.promoter.fs.Glob(filepath.Join(s.promoter.tempDir, ".codarr-*"))
	if err != nil {
		s.errs = append(s.errs, err)
	}

	for _, m := range matches {
		s.remove(m)
	}
}

func (s *sweeper) remove(path string) {
	if s.held[filepath.Clean(path)] {
		return
	}

	if err := s.promoter.fs.Remove(path); err != nil {
		s.errs = append(s.errs, err)
		s.promoter.log.WarnContext(s.ctx, "orphan sweep could not remove an entry",
			slog.String("path", path), slog.Any("error", err))

		return
	}

	s.removed = append(s.removed, path)
	s.promoter.log.InfoContext(s.ctx, "orphan sweep removed a leftover", slog.String("path", path))
}

func isDebris(name string) bool {
	return strings.HasPrefix(name, StagingPrefix) || strings.HasPrefix(name, writeProbePrefix)
}
