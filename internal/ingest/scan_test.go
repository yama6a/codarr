package ingest_test

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/ingest/mock"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// tree is a tiny in-memory filesystem: an entry per absolute path, directories
// implied by the paths beneath them.
type tree struct {
	files   map[string]fsx.FileInfo
	dirs    map[string]bool
	statErr map[string]error
	walkErr error
}

func newTree(root string) *tree {
	return &tree{
		files:   map[string]fsx.FileInfo{},
		dirs:    map[string]bool{root: true},
		statErr: map[string]error{},
	}
}

func (t *tree) add(p string, size int64, mtime time.Time) *tree {
	t.files[p] = fsx.FileInfo{Size: size, MTime: mtime, NLink: 1}

	for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
		t.dirs[d] = true
	}

	return t
}

func (t *tree) fs() *mock.FSMock {
	return &mock.FSMock{
		StatFunc: func(p string) (fsx.FileInfo, error) {
			if err, ok := t.statErr[p]; ok {
				return fsx.FileInfo{}, err
			}

			if t.dirs[p] {
				return fsx.FileInfo{IsDir: true}, nil
			}

			if info, ok := t.files[p]; ok {
				return info, nil
			}

			return fsx.FileInfo{}, fs.ErrNotExist
		},
		WalkDirFunc: func(root string, fn func(string, fsx.FileInfo, error) error) error {
			if t.walkErr != nil {
				return fn(root, fsx.FileInfo{}, t.walkErr)
			}

			return t.walk(root, fn)
		},
	}
}

func (t *tree) walk(root string, fn func(string, fsx.FileInfo, error) error) error {
	paths := make([]string, 0, len(t.files)+len(t.dirs))

	for d := range t.dirs {
		paths = append(paths, d)
	}

	for f := range t.files {
		paths = append(paths, f)
	}

	sort.Strings(paths)

	var skipped []string

	for _, p := range paths {
		if p != root && !strings.HasPrefix(p, root+"/") {
			continue
		}

		if under(p, skipped) {
			continue
		}

		info := fsx.FileInfo{IsDir: true}
		if f, ok := t.files[p]; ok {
			info = f
		}

		err := fn(p, info, nil)
		if errors.Is(err, fs.SkipDir) {
			skipped = append(skipped, p)

			continue
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func under(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre+"/") {
			return true
		}
	}

	return false
}

type scanState struct {
	known   []store.MediaStat
	missing []int64
}

func scanStore(state *scanState) *mock.ScanStoreMock {
	return &mock.ScanStoreMock{
		GetSettingsFunc: func(context.Context) (domain.Settings, error) { return settings(), nil },
		ListRootsFunc:   func(context.Context) ([]domain.Root, error) { return roots()[:1], nil },
		GetRootFunc: func(_ context.Context, id int64) (domain.Root, error) {
			return domain.Root{ID: id, Path: moviesRoot, Enabled: true}, nil
		},
		ListMediaStatsByRootFunc: func(context.Context, int64) ([]store.MediaStat, error) {
			return state.known, nil
		},
		MarkMediaMissingFunc: func(_ context.Context, ids []int64) (int64, error) {
			state.missing = append(state.missing, ids...)

			return int64(len(ids)), nil
		},
	}
}

func recordingAnalyzer() (*mock.FileAnalyzerMock, *[]string) {
	seen := &[]string{}

	return &mock.FileAnalyzerMock{
		AnalyzeInFunc: func(_ context.Context, p string, _ ingest.Env) (ingest.Result, error) {
			*seen = append(*seen, p)

			return ingest.Result{Path: p, MediaFileID: 1, Queued: true}, nil
		},
	}, seen
}

func newScanner(t *testing.T, fsm ingest.FS, st ingest.ScanStore, an ingest.FileAnalyzer) *ingest.Scanner {
	t.Helper()

	return ingest.NewScanner(fsm, st, an, clock.NewFake(now), discardLogger())
}

func TestScanner_ScanAllAnalysesNewFilesAndSkipsExcludedOnes(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	tr := newTree(moviesRoot).
		add(moviesRoot+"/Heat (1995)/Heat (1995).mkv", big, old).
		add(moviesRoot+"/Heat (1995)/Heat (1995)-trailer.mkv", big, old).
		add(moviesRoot+"/Heat (1995)/poster.jpg", 4096, old).
		add(moviesRoot+"/Heat (1995)/Featurettes/Making Of.mkv", big, old).
		add(moviesRoot+"/Heat (1995)/.codarr-staging-1-Heat (1995).mkv", big, old)

	an, seen := recordingAnalyzer()
	state := &scanState{}

	report, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Equal(t, []string{moviesRoot + "/Heat (1995)/Heat (1995).mkv"}, *seen,
		"the extras directory is pruned, so its contents are never even walked")

	require.Equal(t, 1, report.Roots)
	require.Equal(t, 4, report.Walked)
	require.Equal(t, 1, report.Analyzed)
	require.Equal(t, 1, report.Queued)
	require.Equal(t, 3, report.Excluded)
	require.Equal(t, 0, report.Missing)
	require.Equal(t, now, report.StartedAt)
	require.Equal(t, now, report.FinishedAt)
}

// plan.md 12: unchanged size and mtime means no re-analysis. This is the whole
// reason media_files exists.
func TestScanner_ScanAllSkipsUnchangedFiles(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	p := moviesRoot + "/Heat (1995)/Heat (1995).mkv"
	tr := newTree(moviesRoot).add(p, big, old)

	an, seen := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{
		{ID: 1, Path: p, SizeBytes: big, MTime: old.Unix(), Status: domain.MediaDone},
	}}

	report, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Empty(t, *seen)
	require.Equal(t, 1, report.Unchanged)
	require.Equal(t, 0, report.Analyzed)
}

func TestScanner_ScanAllReanalysesOnAChangedSizeOrMtime(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	resized := moviesRoot + "/a/Resized.mkv"
	touched := moviesRoot + "/a/Touched.mkv"
	reappeared := moviesRoot + "/a/Reappeared.mkv"
	stillNew := moviesRoot + "/a/StillNew.mkv"

	tr := newTree(moviesRoot).
		add(resized, big, old).
		add(touched, big, old).
		add(reappeared, big, old).
		add(stillNew, big, old)

	an, seen := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{
		{ID: 1, Path: resized, SizeBytes: big - 1, MTime: old.Unix(), Status: domain.MediaDone},
		{ID: 2, Path: touched, SizeBytes: big, MTime: old.Unix() - 1, Status: domain.MediaDone},
		{ID: 3, Path: reappeared, SizeBytes: big, MTime: old.Unix(), Status: domain.MediaMissing},
		{ID: 4, Path: stillNew, SizeBytes: big, MTime: old.Unix(), Status: domain.MediaNew},
	}}

	report, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Equal(t, []string{reappeared, resized, stillNew, touched}, *seen)
	require.Equal(t, 4, report.Analyzed)
	require.Equal(t, 0, report.Unchanged)
}

// plan.md 13.2: a file written in the last two minutes may still be being
// written, and the next scan catches it.
func TestScanner_ScanAllSkipsFilesInsideTheStabilityWindow(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot).
		add(moviesRoot+"/a/Fresh.mkv", big, now.Add(-time.Minute)).
		add(moviesRoot+"/a/Settled.mkv", big, now.Add(-ingest.StabilityWindow))

	an, seen := recordingAnalyzer()

	report, err := newScanner(t, tr.fs(), scanStore(&scanState{}), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Equal(t, []string{moviesRoot + "/a/Settled.mkv"}, *seen)
	require.Equal(t, 1, report.Unstable)
}

// plan.md 13.2: the row and its history are kept, only the status changes.
func TestScanner_ScanAllMarksVanishedFilesMissing(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	present := moviesRoot + "/a/Present.mkv"
	tr := newTree(moviesRoot).add(present, big, old)

	an, _ := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{
		{ID: 1, Path: present, SizeBytes: big, MTime: old.Unix(), Status: domain.MediaDone},
		{ID: 2, Path: moviesRoot + "/a/Gone.mkv", Status: domain.MediaDone},
		{ID: 3, Path: moviesRoot + "/a/AlreadyGone.mkv", Status: domain.MediaMissing},
	}}

	report, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Equal(t, []int64{2}, state.missing)
	require.Equal(t, 1, report.Missing)
}

// A row for a file the walk never visits, because its directory is pruned, is
// still on disk. Marking it missing would drift the dashboard the other way.
func TestScanner_ScanAllDoesNotPruneAFileThatStillStats(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	hidden := moviesRoot + "/Heat/Featurettes/Making Of.mkv"

	tr := newTree(moviesRoot).
		add(moviesRoot+"/Heat/Heat.mkv", big, old).
		add(hidden, big, old)

	an, _ := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{
		{ID: 9, Path: hidden, Status: domain.MediaDone},
	}}

	_, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Empty(t, state.missing)
}

// An unmounted NFS export walks as an empty tree, which would retire the whole
// library. The root is checked before anything is pruned against it.
func TestScanner_ScanAllRefusesToPruneAgainstAnUnreadableRoot(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot)
	tr.statErr[moviesRoot] = errors.New("stale NFS file handle")

	an, _ := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{
		{ID: 1, Path: moviesRoot + "/a/Heat.mkv", Status: domain.MediaDone},
	}}

	report, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err, "one bad root does not fail the whole pass")

	require.Empty(t, state.missing)
	require.Equal(t, 0, report.Missing)
	require.Equal(t, 1, report.Roots)
}

func TestScanner_ScanAllRefusesARootThatIsNotADirectory(t *testing.T) {
	t.Parallel()

	tr := newTree("/nowhere")
	tr.files[moviesRoot] = fsx.FileInfo{Size: 10}

	an, _ := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{{ID: 1, Path: "/x", Status: domain.MediaDone}}}

	_, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)
	require.Empty(t, state.missing)
}

func TestScanner_ScanAllCountsAFailedAnalysisWithoutStopping(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	tr := newTree(moviesRoot).
		add(moviesRoot+"/a/Bad.mkv", big, old).
		add(moviesRoot+"/a/Good.mkv", big, old)

	an := &mock.FileAnalyzerMock{
		AnalyzeInFunc: func(_ context.Context, p string, _ ingest.Env) (ingest.Result, error) {
			if strings.HasSuffix(p, "Bad.mkv") {
				return ingest.Result{}, errors.New("moov atom not found")
			}

			return ingest.Result{Path: p}, nil
		},
	}

	report, err := newScanner(t, tr.fs(), scanStore(&scanState{}), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Equal(t, 1, report.Failed)
	require.Equal(t, 1, report.Analyzed)
}

func TestScanner_ScanAllCountsAnIgnoredFileAsExcluded(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot).add(moviesRoot+"/a/Heat.mkv", big, now.Add(-time.Hour))

	an := &mock.FileAnalyzerMock{
		AnalyzeInFunc: func(_ context.Context, p string, _ ingest.Env) (ingest.Result, error) {
			return ingest.Result{Path: p, Excluded: ingest.ExcludedIgnored}, nil
		},
	}

	report, err := newScanner(t, tr.fs(), scanStore(&scanState{}), an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Equal(t, 1, report.Excluded)
	require.Equal(t, 0, report.Analyzed)
}

func TestScanner_ScanAllSkipsDisabledRoots(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot).add(moviesRoot+"/a/Heat.mkv", big, now.Add(-time.Hour))

	st := scanStore(&scanState{})
	st.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return []domain.Root{{ID: 1, Path: moviesRoot, Enabled: false}}, nil
	}

	an, seen := recordingAnalyzer()

	report, err := newScanner(t, tr.fs(), st, an).ScanAll(t.Context())
	require.NoError(t, err)

	require.Empty(t, *seen)
	require.Equal(t, 0, report.Roots)
}

func TestScanner_ScanRootIsTheManualPerRootTrigger(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot).add(moviesRoot+"/a/Heat.mkv", big, now.Add(-time.Hour))
	an, seen := recordingAnalyzer()

	report, err := newScanner(t, tr.fs(), scanStore(&scanState{}), an).ScanRoot(t.Context(), 1)
	require.NoError(t, err)

	require.Equal(t, []string{moviesRoot + "/a/Heat.mkv"}, *seen)
	require.Equal(t, 1, report.Roots)
	require.Equal(t, 1, report.Analyzed)
}

func TestScanner_ScanRootSurfacesAnUnreadableRoot(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot)
	tr.statErr[moviesRoot] = errors.New("stale NFS file handle")

	an, _ := recordingAnalyzer()

	_, err := newScanner(t, tr.fs(), scanStore(&scanState{}), an).ScanRoot(t.Context(), 1)
	require.ErrorIs(t, err, ingest.ErrRootUnreadable)
}

func TestScanner_ScanAllStopsOnCancellation(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot).add(moviesRoot+"/a/Heat.mkv", big, now.Add(-time.Hour))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	an, seen := recordingAnalyzer()

	_, err := newScanner(t, tr.fs(), scanStore(&scanState{}), an).ScanAll(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, *seen)
}

// plan.md 13.2: rate-limited so it does not thrash the array. The fake clock
// makes the pacing observable without anything sleeping.
func TestScanner_ScanAllPacesAnalysisAtTheConfiguredRate(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	tr := newTree(moviesRoot)

	for _, n := range []string{"a", "b", "c", "d"} {
		tr.add(moviesRoot+"/x/"+n+".mkv", big, old)
	}

	st := scanStore(&scanState{})
	st.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		s := settings()
		s.ScanRateLimitFPS = 2

		return s, nil
	}

	clk := clock.NewFake(now)
	an, seen := recordingAnalyzer()

	report, err := ingest.NewScanner(tr.fs(), st, an, clk, discardLogger()).ScanAll(t.Context())
	require.NoError(t, err)

	require.Len(t, *seen, 4)
	require.Equal(t, 4, report.Analyzed)
	// Four files at two per second: the first runs immediately, then three
	// half-second waits.
	require.Equal(t, now.Add(1500*time.Millisecond), clk.Now())
}

func TestScanner_ScanAllDoesNotPaceWhenTheRateLimitIsOff(t *testing.T) {
	t.Parallel()

	old := now.Add(-24 * time.Hour)
	tr := newTree(moviesRoot).
		add(moviesRoot+"/x/a.mkv", big, old).
		add(moviesRoot+"/x/b.mkv", big, old)

	st := scanStore(&scanState{})
	st.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		s := settings()
		s.ScanRateLimitFPS = 0

		return s, nil
	}

	clk := clock.NewFake(now)
	an, _ := recordingAnalyzer()

	_, err := ingest.NewScanner(tr.fs(), st, an, clk, discardLogger()).ScanAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, now, clk.Now())
}

func TestScanner_ScanAllSurfacesASettingsFailure(t *testing.T) {
	t.Parallel()

	st := scanStore(&scanState{})
	st.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		return domain.Settings{}, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := newScanner(t, newTree(moviesRoot).fs(), st, an).ScanAll(t.Context())
	require.ErrorContains(t, err, "get settings: database is locked")
}

func TestScanner_ScanAllSurvivesAWalkErrorBelowTheRoot(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot).add(moviesRoot+"/a/Heat.mkv", big, now.Add(-time.Hour))
	tr.walkErr = errors.New("permission denied")

	an, _ := recordingAnalyzer()
	state := &scanState{known: []store.MediaStat{{ID: 1, Path: "/x", Status: domain.MediaDone}}}

	report, err := newScanner(t, tr.fs(), scanStore(state), an).ScanAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, report.Walked)
	require.Empty(t, state.missing, "a failed walk never prunes")
}

func TestScanner_ScanAllSurfacesAPruneFailure(t *testing.T) {
	t.Parallel()

	tr := newTree(moviesRoot)
	state := &scanState{known: []store.MediaStat{
		{ID: 1, Path: moviesRoot + "/Gone.mkv", Status: domain.MediaDone},
	}}

	st := scanStore(state)
	st.MarkMediaMissingFunc = func(context.Context, []int64) (int64, error) {
		return 0, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	report, err := newScanner(t, tr.fs(), st, an).ScanAll(t.Context())
	require.NoError(t, err, "one bad root does not fail the whole pass")
	require.Equal(t, 0, report.Missing)
}

func TestScanner_ScanAllSurfacesAKnownFilesFailure(t *testing.T) {
	t.Parallel()

	st := scanStore(&scanState{})
	st.ListMediaStatsByRootFunc = func(context.Context, int64) ([]store.MediaStat, error) {
		return nil, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := newScanner(t, newTree(moviesRoot).fs(), st, an).ScanRoot(t.Context(), 1)
	require.ErrorContains(t, err, "list known files under "+moviesRoot)
}

func TestScanner_ScanAllSurfacesARootsFailure(t *testing.T) {
	t.Parallel()

	st := scanStore(&scanState{})
	st.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return nil, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := newScanner(t, newTree(moviesRoot).fs(), st, an).ScanAll(t.Context())
	require.ErrorContains(t, err, "list roots: database is locked")
}

func TestScanner_ScanRootSurfacesAMissingRoot(t *testing.T) {
	t.Parallel()

	st := scanStore(&scanState{})
	st.GetRootFunc = func(context.Context, int64) (domain.Root, error) {
		return domain.Root{}, store.ErrNotFound
	}

	an, _ := recordingAnalyzer()

	_, err := newScanner(t, newTree(moviesRoot).fs(), st, an).ScanRoot(t.Context(), 404)
	require.ErrorIs(t, err, store.ErrNotFound)
}
