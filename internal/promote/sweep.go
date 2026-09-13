package promote

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/fsx"
	"go.uber.org/zap"
)

// Sweep removes staging files and write probes no live job claims; it runs after
// the job-state sweep of 19.2, so anything left unclaimed is debris from a crash.
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
		s.promoter.log.Warn("orphan sweep could not remove an entry", zap.String("path", path), zap.Error(err))

		return
	}

	s.removed = append(s.removed, path)
	s.promoter.log.Info("orphan sweep removed a leftover", zap.String("path", path))
}

func isDebris(name string) bool {
	return strings.HasPrefix(name, StagingPrefix) || strings.HasPrefix(name, writeProbePrefix)
}
