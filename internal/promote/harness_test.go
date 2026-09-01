package promote_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/promote"
	"github.com/yama6a/codarr/internal/promote/mock"
)

const (
	sourcePath  = "/media/yama/movies/Dune (2021)/Dune.mkv"
	destDir     = "/media/yama/movies/Dune (2021)"
	tempDir     = "/scratch"
	jobID       = int64(42)
	stagingPath = destDir + "/.codarr-staging-42.mkv"
	sourceSize  = int64(8 << 30)
)

// recorder is the ordered log of every filesystem and Plex call, because plan.md
// 15.6's "nothing between the check and the rename" is an assertion about order.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(op, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, op+" "+path)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.calls...)
}

type fakeFile struct {
	data   []byte
	size   int64
	mode   os.FileMode
	mtime  time.Time
	uid    int
	gid    int
	nlink  int
	device uint64
	dir    bool
}

type fakeFS struct {
	mu sync.Mutex

	rec     *recorder
	files   map[string]*fakeFile
	space   map[string]fsx.SpaceInfo
	errs    map[string]error
	synced  []string
	walkErr map[string]error

	// nextDevice overrides the device reported by the next Stat of a path, which
	// is how a mid-flight dataset split is simulated.
	nextDevice map[string][]uint64
}

var _ fsx.FS = (*fakeFS)(nil)

func newFakeFS(rec *recorder) *fakeFS {
	return &fakeFS{
		rec:        rec,
		files:      map[string]*fakeFile{},
		space:      map[string]fsx.SpaceInfo{},
		errs:       map[string]error{},
		walkErr:    map[string]error{},
		nextDevice: map[string][]uint64{},
	}
}

func (f *fakeFS) addDir(path string) *fakeFile {
	return f.put(path, &fakeFile{mode: 0o755 | os.ModeDir, dir: true, nlink: 2, device: 1})
}

func (f *fakeFS) addFile(path string, size int64) {
	f.put(path, &fakeFile{
		size:   size,
		mode:   0o640,
		mtime:  time.Unix(1700000000, 0).UTC(),
		uid:    568,
		gid:    568,
		nlink:  1,
		device: 1,
	})
}

func (f *fakeFS) put(path string, file *fakeFile) *fakeFile {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.files[path] = file

	return file
}

func (f *fakeFS) get(path string) (*fakeFile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	file, ok := f.files[path]

	return file, ok
}

func (f *fakeFS) failOn(op, path string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.errs[op+" "+path] = err
}

func (f *fakeFS) injected(op, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.errs[op+" "+path]
}

func (f *fakeFS) call(op, path string) error {
	f.rec.add(op, path)

	return f.injected(op, path)
}

func (f *fakeFS) Stat(path string) (fsx.FileInfo, error) {
	if err := f.call("fs.Stat", path); err != nil {
		return fsx.FileInfo{}, err
	}

	file, ok := f.get(path)
	if !ok {
		return fsx.FileInfo{}, os.ErrNotExist
	}

	return fsx.FileInfo{
		Size:   file.size,
		Mode:   file.mode,
		MTime:  file.mtime,
		UID:    file.uid,
		GID:    file.gid,
		NLink:  file.nlink,
		Device: f.device(path, file.device),
		IsDir:  file.dir,
	}, nil
}

func (f *fakeFS) device(path string, fallback uint64) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	queued := f.nextDevice[path]
	if len(queued) == 0 {
		return fallback
	}

	f.nextDevice[path] = queued[1:]

	return queued[0]
}

func (f *fakeFS) Statfs(path string) (fsx.SpaceInfo, error) {
	if err := f.call("fs.Statfs", path); err != nil {
		return fsx.SpaceInfo{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	space, ok := f.space[path]
	if !ok {
		return fsx.SpaceInfo{}, os.ErrNotExist
	}

	return space, nil
}

func (f *fakeFS) Open(path string) (io.ReadSeekCloser, error) {
	if err := f.call("fs.Open", path); err != nil {
		return nil, err
	}

	file, ok := f.get(path)
	if !ok {
		return nil, os.ErrNotExist
	}

	return nopCloser{bytes.NewReader(file.data)}, nil
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func (f *fakeFS) Create(path string, mode os.FileMode) (fsx.WriteSyncCloser, error) {
	if err := f.call("fs.Create", path); err != nil {
		return nil, err
	}

	if _, ok := f.get(path); ok {
		return nil, os.ErrExist
	}

	f.put(path, &fakeFile{mode: mode, mtime: time.Unix(1700000000, 0).UTC(), nlink: 1, device: 1})

	return &fakeHandle{fs: f, path: path}, nil
}

type fakeHandle struct {
	fs   *fakeFS
	path string
}

func (h *fakeHandle) Write(p []byte) (int, error) {
	file, ok := h.fs.get(h.path)
	if !ok {
		return 0, os.ErrNotExist
	}

	file.data = append(file.data, p...)
	file.size = int64(len(file.data))

	return len(p), nil
}

func (h *fakeHandle) Sync() error  { return h.fs.call("fs.SyncFile", h.path) }
func (h *fakeHandle) Close() error { return h.fs.call("fs.Close", h.path) }

func (f *fakeFS) Copy(ctx context.Context, src, dst string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("copy cancelled: %w", err)
	}

	if err := f.call("fs.Copy", src+" -> "+dst); err != nil {
		return 0, err
	}

	file, ok := f.get(src)
	if !ok {
		return 0, os.ErrNotExist
	}

	f.put(dst, &fakeFile{
		data: file.data, size: file.size, mode: 0o644,
		mtime: file.mtime, nlink: 1, device: 1,
	})

	return file.size, nil
}

func (f *fakeFS) Remove(path string) error {
	if err := f.call("fs.Remove", path); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files[path]; !ok {
		return os.ErrNotExist
	}

	delete(f.files, path)

	return nil
}

func (f *fakeFS) Rename(oldpath, newpath string) error {
	if err := f.call("fs.Rename", oldpath+" -> "+newpath); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	file, ok := f.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}

	delete(f.files, oldpath)
	f.files[newpath] = file

	return nil
}

func (f *fakeFS) SyncFile(path string) error {
	if err := f.call("fs.SyncFile", path); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.synced = append(f.synced, path)

	return nil
}

func (f *fakeFS) SyncDir(path string) error {
	if err := f.call("fs.SyncDir", path); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.synced = append(f.synced, path)

	return nil
}

func (f *fakeFS) Chmod(path string, mode os.FileMode) error {
	if err := f.call("fs.Chmod", path); err != nil {
		return err
	}

	file, ok := f.get(path)
	if !ok {
		return os.ErrNotExist
	}

	file.mode = mode

	return nil
}

func (f *fakeFS) Chtimes(path string, _, mtime time.Time) error {
	if err := f.call("fs.Chtimes", path); err != nil {
		return err
	}

	file, ok := f.get(path)
	if !ok {
		return os.ErrNotExist
	}

	file.mtime = mtime

	return nil
}

func (f *fakeFS) Chown(path string, uid, gid int) error {
	if err := f.call("fs.Chown", path); err != nil {
		return err
	}

	file, ok := f.get(path)
	if !ok {
		return os.ErrNotExist
	}

	file.uid, file.gid = uid, gid

	return nil
}

func (f *fakeFS) WalkDir(root string, fn func(string, fsx.FileInfo, error) error) error {
	f.rec.add("fs.WalkDir", root)

	f.mu.Lock()

	var paths []string

	for p := range f.files {
		if p == root || strings.HasPrefix(p, root+"/") {
			paths = append(paths, p)
		}
	}

	walkErrs := f.walkErr
	f.mu.Unlock()

	sort.Strings(paths)

	for _, p := range paths {
		if err, ok := walkErrs[p]; ok {
			if cbErr := fn(p, fsx.FileInfo{}, err); cbErr != nil {
				return cbErr
			}

			continue
		}

		info, err := f.Stat(p)
		if err != nil {
			return err
		}

		if cbErr := fn(p, info, nil); cbErr != nil {
			return cbErr
		}
	}

	return f.injected("fs.WalkDir", root)
}

func (f *fakeFS) MkdirAll(path string, mode os.FileMode) error {
	if err := f.call("fs.MkdirAll", path); err != nil {
		return err
	}

	f.put(path, &fakeFile{mode: mode | os.ModeDir, dir: true, nlink: 2, device: 1})

	return nil
}

func (f *fakeFS) Glob(pattern string) ([]string, error) {
	if err := f.call("fs.Glob", pattern); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for p := range f.files {
		if ok, _ := filepath.Match(pattern, p); ok {
			out = append(out, p)
		}
	}

	sort.Strings(out)

	return out, nil
}

// harness wires a Promoter with a fake filesystem and moq'd collaborators, all
// logging into one ordered recorder.
type harness struct {
	t        *testing.T
	deps     promote.Deps
	fs       *fakeFS
	rec      *recorder
	prober   *mock.ProberMock
	guard    *mock.StreamGuardMock
	fp       *mock.FingerprinterMock
	notifier *mock.NotifierMock
	copier   *mock.CopierMock
	clk      *clock.Fake
	promoter *promote.Promoter
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	rec := &recorder{}
	ffs := newFakeFS(rec)

	h := &harness{
		t:   t,
		fs:  ffs,
		rec: rec,
		prober: &mock.ProberMock{ProbeFunc: func(_ context.Context, path string) (promote.Output, error) {
			rec.add("ffprobe.Probe", path)

			return goodOutput(), nil
		}},
		guard: &mock.StreamGuardMock{IsStreamingFunc: func(_ context.Context, path string) (bool, string, error) {
			rec.add("plex.IsStreaming", path)

			return false, "", nil
		}},
		fp: &mock.FingerprinterMock{
			SparseFunc: func(path string) (string, error) {
				rec.add("fingerprint.Sparse", path)

				return "xxh3-128:" + strings.Repeat("a", 32), nil
			},
			FullFunc: func(path string) (string, error) {
				rec.add("fingerprint.Full", path)

				return "xxh3-128:" + strings.Repeat("b", 32), nil
			},
		},
		notifier: &mock.NotifierMock{NotifyPromotedFunc: func(_ context.Context, path string) error {
			rec.add("notify.Promoted", path)

			return nil
		}},
		copier: &mock.CopierMock{CopyFunc: func(ctx context.Context, src, dst string) (int64, error) {
			return promote.NewFSCopier(ffs).Copy(ctx, src, dst)
		}},
		clk: clock.NewFake(time.Unix(1700009999, 0).UTC()),
	}

	h.deps = promote.Deps{
		FS:            ffs,
		Clock:         h.clk,
		Prober:        h.prober,
		Guard:         h.guard,
		Fingerprinter: h.fp,
		Notifier:      h.notifier,
		Copier:        h.copier,
		Logger:        slog.New(slog.DiscardHandler),
		TempDir:       tempDir,
	}
	h.promoter = promote.New(h.deps)

	ffs.addDir(destDir)
	ffs.addDir(tempDir)
	ffs.addFile(sourcePath, sourceSize)
	ffs.addFile(stagingPath, sourceSize/2)
	ffs.space[destDir] = fsx.SpaceInfo{TotalBytes: 100 << 30, FreeBytes: 50 << 30}
	ffs.space[tempDir] = fsx.SpaceInfo{TotalBytes: 100 << 30, FreeBytes: 50 << 30}

	return h
}

// rebuild re-wires the Promoter after a dependency was changed.
func (h *harness) rebuild(mutate func(*promote.Deps)) {
	h.t.Helper()

	mutate(&h.deps)
	h.promoter = promote.New(h.deps)
}

// probeReturns rewrites what ffprobe reports for the staged output.
func (h *harness) probeReturns(mutate func(*promote.Output)) {
	h.t.Helper()

	h.prober.ProbeFunc = func(_ context.Context, path string) (promote.Output, error) {
		h.rec.add("ffprobe.Probe", path)

		out := goodOutput()
		mutate(&out)

		return out, nil
	}
}

func (h *harness) withoutTempDir() {
	h.rebuild(func(d *promote.Deps) { d.TempDir = "" })
}

func sourceState() promote.SourceState {
	return promote.SourceState{
		SizeBytes:       sourceSize,
		MTime:           time.Unix(1700000000, 0).UTC().Unix(),
		Fingerprint:     "xxh3-128:" + strings.Repeat("a", 32),
		DurationSeconds: 5121,
		Video: &domain.VideoState{
			Codec: "hevc", Profile: "Main 10", Level: "5.1", Width: 3840, Height: 2160,
		},
	}
}

func outIdx(i int) *int { return &i }

// goodPlan is an audio_only plan: video copied, one English AC3 track, one
// subtitle. Three output streams.
func goodPlan() domain.Plan {
	return domain.Plan{
		Kind:            domain.KindAudioOnly,
		OutputContainer: domain.ContainerMatroska,
		PolicyHash:      "policy-abc",
		Streams: []domain.StreamPlan{
			{Type: domain.StreamVideo, SourceIndex: 0, OutputIndex: outIdx(0), Decision: domain.DecisionCopy},
			{Type: domain.StreamAudio, SourceIndex: 1, OutputIndex: outIdx(1), Decision: domain.DecisionConvert, Language: "eng"},
			{Type: domain.StreamSubtitle, SourceIndex: 2, OutputIndex: outIdx(2), Decision: domain.DecisionCopy, Language: "eng"},
			{Type: domain.StreamSubtitle, SourceIndex: 3, Decision: domain.DecisionDrop, Language: "dut"},
		},
	}
}

func goodOutput() promote.Output {
	return promote.Output{
		DurationSeconds: 5120,
		Streams: []promote.OutputStream{
			{Type: domain.StreamVideo, Codec: "hevc", Profile: "Main 10", Level: "5.1", Width: 3840, Height: 2160},
			{Type: domain.StreamAudio, Codec: "ac3", Language: "eng"},
			{Type: domain.StreamSubtitle, Codec: "subrip", Language: "eng"},
		},
	}
}

func request() promote.Request {
	return promote.Request{
		JobID:      jobID,
		SourcePath: sourcePath,
		Staging:    promote.Staging{Path: stagingPath, FinalPath: stagingPath},
		Plan:       goodPlan(),
		Source:     sourceState(),
	}
}

func requireFailure(t *testing.T, err error, code domain.FailureCode, contains ...string) {
	t.Helper()

	var perr *promote.Error

	require.ErrorAs(t, err, &perr)
	require.Equal(t, code, perr.Code)
	require.NotEmpty(t, perr.Message)

	for _, want := range contains {
		require.Contains(t, perr.Error(), want)
	}
}

// requireRenameFollowsTheFinalCheck is the plan.md 15.6 assertion: the rename is
// the very next thing recorded after the last Plex check, with nothing between.
func requireRenameFollowsTheFinalCheck(t *testing.T, calls []string) {
	t.Helper()

	check := "plex.IsStreaming " + sourcePath
	rename := "fs.Rename " + stagingPath + " -> " + sourcePath
	last := -1

	for i, c := range calls {
		if c == check {
			last = i
		}
	}

	require.NotEqual(t, -1, last, "call %q never happened in %v", check, calls)
	require.Less(t, last, len(calls)-1, "nothing recorded after %q", check)
	require.Equal(t, rename, calls[last+1], "expected %q immediately after %q, got %v", rename, check, calls)
}
