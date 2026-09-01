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

	var (
		removed []string
		errs    []error
	)

	remove := func(path string) {
		if held[filepath.Clean(path)] {
			return
		}

		if err := p.fs.Remove(path); err != nil {
			errs = append(errs, err)
			p.log.WarnContext(ctx, "orphan sweep could not remove an entry",
				slog.String("path", path), slog.Any("error", err))

			return
		}

		removed = append(removed, path)
		p.log.InfoContext(ctx, "orphan sweep removed a leftover", slog.String("path", path))
	}

	for _, root := range roots {
		err := p.fs.WalkDir(root, func(path string, _ fsx.FileInfo, err error) error {
			if err != nil {
				errs = append(errs, err)

				return nil
			}

			if isDebris(filepath.Base(path)) {
				remove(path)
			}

			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}

	if p.tempDir != "" {
		matches, err := p.fs.Glob(filepath.Join(p.tempDir, ".codarr-*"))
		if err != nil {
			errs = append(errs, err)
		}

		for _, m := range matches {
			remove(m)
		}
	}

	sort.Strings(removed)

	return removed, errors.Join(errs...)
}

func isDebris(name string) bool {
	return strings.HasPrefix(name, StagingPrefix) || strings.HasPrefix(name, writeProbePrefix)
}
