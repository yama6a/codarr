package job_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/job/mock"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/promote"
)

const (
	mediaID     = int64(7)
	sourcePath  = "/media/movies/Example (2019)/Example.mkv"
	destDir     = "/media/movies/Example (2019)"
	tempDir     = "/scratch"
	stagingPath = destDir + "/.codarr-staging-1.mkv"
	legacyPath  = destDir + "/Example.avi"
	sourceSize  = int64(9871234567)
	sourceMTime = int64(1700000000)
	sourceFP    = "xxh3-128:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	outputFP    = "xxh3-128:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mediaDur    = 7200.0
)

// harness wires a Service with the in-memory store and moq'd collaborators, every
// default on the happy path so a test changes the one thing it is about.
type harness struct {
	t        *testing.T
	store    *fakeStore
	prober   *mock.ProberMock
	encoder  *mock.EncoderMock
	promoter *mock.PromoterMock
	fs       *mock.FSMock
	fp       *mock.FingerprinterMock
	notifier *mock.NotifierMock
	hw       *mock.HardwareMock
	analyzer *mock.AnalyzerMock
	clk      *clock.Fake
	metrics  *fakeMetrics
	svc      *job.Service

	mu      sync.Mutex
	files   map[string]fsx.FileInfo
	removed []string
	runs    [][]string
}

// newHarness wires the queue with no metrics at all, which is the shape most of
// these tests want and which keeps a nil Deps.Metrics under test everywhere.
func newHarness(t *testing.T) *harness {
	t.Helper()

	return newHarnessWith(t, nil)
}

// newMeteredHarness is the same wiring with the Prometheus surface attached.
func newMeteredHarness(t *testing.T) *harness {
	t.Helper()

	return newHarnessWith(t, &fakeMetrics{})
}

func newHarnessWith(t *testing.T, mx *fakeMetrics) *harness {
	t.Helper()

	clk := clock.NewFake(time.Unix(1700009999, 0).UTC())
	h := &harness{
		t:       t,
		store:   newFakeStore(clk.Now),
		clk:     clk,
		metrics: mx,
		files:   map[string]fsx.FileInfo{},
	}

	h.store.settings = domain.Settings{TempDir: tempDir, PrioritiseQuickJobs: true}
	h.store.roots = []domain.Root{{ID: 1, Path: "/media/movies", Enabled: true}}
	h.store.putMedia(mediaFile(audioOnlyProbe()))

	h.addFile(sourcePath, sourceSize)
	h.addDir(destDir)
	h.addDir(tempDir)

	h.prober = &mock.ProberMock{ProbeFunc: func(_ context.Context, _ string) (*ffprobe.Result, error) {
		return parse(t, audioOnlyProbe()), nil
	}}

	h.encoder = &mock.EncoderMock{RunFunc: func(_ context.Context, args []string, progress func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		if progress != nil {
			progress(ffmpeg.Progress{Percent: 100, Speed: 4.2, OutTime: time.Duration(mediaDur) * time.Second})
		}

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}}

	h.promoter = &mock.PromoterMock{
		PreflightFunc: func(req promote.PreflightRequest) (promote.Staging, error) {
			path := filepath.Join(filepath.Dir(req.SourcePath),
				promote.StagingPrefix+strconv.FormatInt(req.JobID, 10)+req.OutputExt)

			return promote.Staging{Path: path, FinalPath: path}, nil
		},
		VerifyFunc: func(context.Context, promote.Request) ([]string, error) { return nil, nil },
		PromoteFunc: func(_ context.Context, req promote.Request) (promote.Result, error) {
			return promote.Result{
				Identity: domain.OutputIdentity{
					Fingerprint: outputFP,
					SizeBytes:   sourceSize / 2,
					MTime:       sourceMTime,
					PolicyHash:  req.Plan.PolicyHash,
					RecordedAt:  clk.Now(),
				},
				OutputSize: sourceSize / 2,
				Renamed:    true,
			}, nil
		},
		SweepFunc: func(context.Context, []string, []string) ([]string, error) { return nil, nil },
	}

	h.fs = &mock.FSMock{
		StatFunc:     h.stat,
		RemoveFunc:   h.remove,
		MkdirAllFunc: func(path string, _ os.FileMode) error { h.addDir(path); return nil },
	}

	h.fp = &mock.FingerprinterMock{SparseFunc: func(string) (string, error) { return sourceFP, nil }}
	h.notifier = &mock.NotifierMock{NotifyPromotedFunc: func(context.Context, string) error { return nil }}
	h.hw = &mock.HardwareMock{CapabilitiesFunc: func(context.Context) (hardware.Capabilities, error) {
		return workingQSV(), nil
	}}
	h.analyzer = &mock.AnalyzerMock{AnalyzeFunc: func(_ context.Context, m domain.MediaFile) (domain.MediaFile, error) {
		return m, nil
	}}

	deps := job.Deps{
		Store:         h.store,
		Prober:        h.prober,
		Promoter:      h.promoter,
		FS:            h.fs,
		Fingerprinter: h.fp,
		Notifier:      h.notifier,
		Hardware:      h.hw,
		Analyzer:      h.analyzer,
		NewEncoder:    func(time.Duration) job.Encoder { return h.encoder },
		Clock:         clk,
		Logger:        slog.New(slog.DiscardHandler),
		Version:       "1.2.3",
		IdlePoll:      time.Millisecond,
	}

	// Assigned rather than passed, so an absent fake leaves Deps.Metrics a true nil
	// rather than a typed nil in the interface.
	if mx != nil {
		deps.Metrics = mx
	}

	h.svc = job.New(deps)

	return h
}

// workingQSV is the expected result on a UHD 630: QSV encodes both HEVC
// profiles, VAAPI is there as the fallback, and the driver delivers VP9 decode.
func workingQSV() hardware.Capabilities {
	entries := []domain.HWCapability{
		{Backend: "qsv", Codec: "hevc", Profile: "main", Direction: "encode", Works: true},
		{Backend: "qsv", Codec: "hevc", Profile: "main10", Direction: "encode", Works: true},
		{Backend: "vaapi", Codec: "hevc", Profile: "main", Direction: "encode", Works: true},
		{Backend: "vaapi", Codec: "hevc", Profile: "main10", Direction: "encode", Works: true},
		{Backend: "qsv", Codec: "vp9", Direction: "decode", Works: true},
	}

	return hardware.Capabilities{
		Device:        "/dev/dri/renderD128",
		FfmpegVersion: "7.1.4-Jellyfin",
		ProbedAt:      time.Unix(1700000000, 0).UTC(),
		Entries:       entries,
	}
}

// softwareOnly is a host where neither backend works, which is the case
// plan.md 10.2 says has to be impossible to miss.
func softwareOnly() hardware.Capabilities {
	return hardware.Capabilities{
		Device:        "/dev/dri/renderD128",
		FfmpegVersion: "7.1.4-Jellyfin",
		ProbedAt:      time.Unix(1700000000, 0).UTC(),
		Entries: []domain.HWCapability{
			{Backend: "qsv", Codec: "hevc", Profile: "main", Direction: "encode", Works: false},
			{Backend: "vaapi", Codec: "hevc", Profile: "main", Direction: "encode", Works: false},
		},
	}
}

func (h *harness) addFile(path string, size int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.files[path] = fsx.FileInfo{Size: size, Mode: 0o640, MTime: time.Unix(sourceMTime, 0).UTC(), NLink: 1, Device: 1}
}

func (h *harness) addDir(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.files[path] = fsx.FileInfo{Mode: 0o755 | os.ModeDir, NLink: 2, Device: 1, IsDir: true}
}

func (h *harness) setDevice(path string, device uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	info := h.files[path]
	info.Device = device
	h.files[path] = info
}

func (h *harness) stat(path string) (fsx.FileInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	info, ok := h.files[path]
	if !ok {
		return fsx.FileInfo{}, os.ErrNotExist
	}

	return info, nil
}

func (h *harness) remove(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.removed = append(h.removed, path)

	if _, ok := h.files[path]; !ok {
		return os.ErrNotExist
	}

	delete(h.files, path)

	return nil
}

func (h *harness) removedPaths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.removed)
}

func (h *harness) recordRun(args []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.runs = append(h.runs, slices.Clone(args))
}

func (h *harness) runArgs() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.runs)
}

// queue puts one job in the queue for the default media file.
func (h *harness) queue(kind domain.Kind, origin domain.JobOrigin) domain.Job {
	h.t.Helper()

	return h.store.putJob(domain.Job{
		MediaFileID: mediaID,
		Kind:        kind,
		Origin:      origin,
		Priority:    domain.PriorityQuick,
		State:       domain.JobQueued,
		QueuedAt:    h.clk.Now(),
	})
}

// interrupted puts one job in an in-flight state, as a crash would have left it.
func (h *harness) interrupted(state domain.JobState, attempt int) domain.Job {
	h.t.Helper()

	started := h.clk.Now().Add(-time.Hour)

	return h.store.putJob(domain.Job{
		MediaFileID: mediaID,
		Kind:        domain.KindAudioOnly,
		Origin:      domain.OriginIngest,
		Priority:    domain.PriorityQuick,
		State:       state,
		Attempt:     attempt,
		StagingPath: stagingPath,
		StartedAt:   &started,
		Transform:   decide.NewTransform(parse(h.t, audioOnlyProbe()), currentPlan(h.t, audioOnlyProbe()), 60),
		QueuedAt:    h.clk.Now().Add(-2 * time.Hour),
	})
}

// interruptedInTemp is the same as interrupted, but staged in the temp
// directory because the destination lacked space (15.1).
func (h *harness) interruptedInTemp(state domain.JobState) domain.Job {
	h.t.Helper()

	j := h.interrupted(state, 0)
	j.StagingPath = tempDir + "/.codarr-staging-1.mkv"
	j.UsedTempDir = true

	return h.store.putJob(j)
}

func (h *harness) jobRow(id int64) domain.Job {
	h.t.Helper()

	j, ok := h.store.job(id)
	require.True(h.t, ok, "job %d does not exist", id)

	return j
}

func (h *harness) mediaRow() domain.MediaFile {
	h.t.Helper()

	m, err := h.store.GetMediaFile(h.t.Context(), mediaID)
	require.NoError(h.t, err)

	return m
}

// requireFailed asserts plan.md 19.1 in full: a code, and a message specific
// enough to act on.
func (h *harness) requireFailed(id int64, code domain.FailureCode, contains ...string) domain.Job {
	h.t.Helper()

	j := h.jobRow(id)
	require.Equal(h.t, domain.JobFailed, j.State)
	require.Equal(h.t, code, j.FailureCode)
	require.NotEmpty(h.t, j.FailureMessage, "a failed job with an empty message is a bug (19.1)")

	for _, want := range contains {
		require.Contains(h.t, j.FailureMessage, want)
	}

	return j
}

func mediaFile(probe ffprobe.Result) domain.MediaFile {
	return mediaFileAt(sourcePath, probe)
}

func mediaFileAt(path string, probe ffprobe.Result) domain.MediaFile {
	raw, err := json.Marshal(probe)
	if err != nil {
		panic(err)
	}

	analysed := time.Unix(1700001000, 0).UTC()
	parsed, err := ffprobe.Parse(raw)
	if err != nil {
		panic(err)
	}

	analysis, err := decide.New().Plan(parsed, decide.Options{Path: path})
	if err != nil {
		panic(err)
	}

	bitrate, source := decide.ResolveVideoBitrate(parsed)
	video, _ := parsed.PrimaryVideo()

	return domain.MediaFile{
		ID:              mediaID,
		Path:            path,
		SizeBytes:       sourceSize,
		MTime:           sourceMTime,
		NLink:           1,
		Fingerprint:     sourceFP,
		FingerprintAlgo: "xxh3-128",
		ProbeJSON:       string(raw),
		AnalyzedAt:      &analysed,
		Plan:            &analysis.Plan,
		PlanKind:        analysis.Plan.Kind,
		PlanReasons:     analysis.Plan.Reasons,
		Container:       parsed.Format.FormatName,
		VideoCodec:      video.CodecName,
		VideoBitrate:    bitrate,
		VideoBitrateSrc: source,
		Status:          domain.MediaAnalyzed,
	}
}

func parse(t *testing.T, probe ffprobe.Result) *ffprobe.Result {
	t.Helper()

	raw, err := json.Marshal(probe)
	require.NoError(t, err)

	parsed, err := ffprobe.Parse(raw)
	require.NoError(t, err)

	return parsed
}

func currentPlan(t *testing.T, probe ffprobe.Result) domain.Plan {
	t.Helper()

	analysis, err := decide.New().Plan(parse(t, probe), decide.Options{Path: sourcePath})
	require.NoError(t, err)

	return analysis.Plan
}

// audioOnlyProbe is the dominant case of plan.md 7: compliant H.264 video, one
// DTS track that has to be re-encoded, one PGS track that is dropped.
func audioOnlyProbe() ffprobe.Result {
	return ffprobe.Result{
		Format: ffprobe.Format{
			Filename:   sourcePath,
			FormatName: "matroska,webm",
			Duration:   "7200.000000",
			Size:       "9871234567",
			BitRate:    "10968038",
		},
		Streams: []ffprobe.Stream{
			videoStream("h264", "High", 40, "yuv420p"),
			{
				Index: 1, CodecName: "dts", CodecType: "audio", Profile: "DTS-HD MA",
				Channels: 6, ChannelLayout: "5.1(side)",
				Disposition: ffprobe.Disposition{Default: 1},
				Tags:        map[string]string{"language": "eng", "BPS-eng": "1509000"},
			},
			{
				Index: 2, CodecName: "hdmv_pgs_subtitle", CodecType: "subtitle",
				Disposition: ffprobe.Disposition{Forced: 1},
				Tags:        map[string]string{"language": "eng"},
			},
		},
	}
}

// fullProbe is an Xvid file: the codec is off the copy list, so the video is
// re-encoded and the job is the rare full kind.
func fullProbe() ffprobe.Result {
	p := audioOnlyProbe()
	p.Streams[0] = videoStream("mpeg4", "Simple Profile", 5, "yuv420p")
	p.Streams[1] = ffprobe.Stream{
		Index: 1, CodecName: "ac3", CodecType: "audio", Channels: 2, ChannelLayout: "stereo",
		Disposition: ffprobe.Disposition{Default: 1},
		Tags:        map[string]string{"language": "eng", "BPS-eng": "192000"},
	}

	return p
}

// hwFullProbe is a 10-bit H.264 file: off the copy list, so it re-encodes, and
// hardware-decodable, so it takes the hardware decode path (10.1).
func hwFullProbe() ffprobe.Result {
	p := fullProbe()
	p.Streams[0] = videoStream("h264", "High 10", 40, "yuv420p10le")

	return p
}

// idetProbe is an MPEG-2 file with no field_order, which is exactly the case
// plan.md 6.2 says to settle with an idet sample rather than a guess.
func idetProbe() ffprobe.Result {
	p := fullProbe()
	p.Streams[0] = videoStream("mpeg2video", "Main", 8, "yuv420p")

	return p
}

func videoStream(codec, profile string, level int, pixFmt string) ffprobe.Stream {
	return ffprobe.Stream{
		Index: 0, CodecName: codec, CodecType: "video", Profile: profile, Level: level,
		Width: 1920, Height: 1080, CodedWidth: 1920, CodedHeight: 1088,
		PixFmt: pixFmt, RFrameRate: "24000/1001", AvgFrameRate: "24000/1001", Refs: 3,
		Disposition: ffprobe.Disposition{Default: 1},
		Tags:        map[string]string{"language": "eng", "BPS-eng": "8420000"},
	}
}

// legacyProbe is an AVI: a legacy container, which is exactly the input whose
// header duration 15.3 says cannot be trusted.
func legacyProbe() ffprobe.Result {
	p := audioOnlyProbe()
	p.Format.Filename = legacyPath
	p.Format.FormatName = "avi"

	return p
}

// skipProbe is a file that already matches the policy: compliant H.264 video,
// one AC3 stereo track, nothing to convert.
func skipProbe() ffprobe.Result {
	p := audioOnlyProbe()
	p.Streams = []ffprobe.Stream{
		p.Streams[0],
		{
			Index: 1, CodecName: "ac3", CodecType: "audio", Channels: 2, ChannelLayout: "stereo",
			Disposition: ffprobe.Disposition{Default: 1},
			Tags:        map[string]string{"language": "eng", "BPS-eng": "192000"},
		},
	}

	return p
}

// withID copies the default media row onto another id and path, for the bulk
// operations, which are the only things that touch more than one file.
func withID(id int64, probe ffprobe.Result, mutate func(m *domain.MediaFile)) domain.MediaFile {
	m := mediaFile(probe)
	m.ID = id
	m.Path = destDir + "/Example " + strconv.FormatInt(id, 10) + ".mkv"
	m.Status = domain.MediaDone

	if mutate != nil {
		mutate(&m)
	}

	return m
}

// taggedProbe is what an output Codarr already wrote looks like: the global
// tags of plan.md 12, carrying the policy hash that produced it.
func taggedProbe(policy string) ffprobe.Result {
	p := audioOnlyProbe()
	p.Format.Tags = map[string]string{
		decide.TagPresent: "true",
		decide.TagVersion: "1.2.3",
		decide.TagPolicy:  policy,
	}

	return p
}

func containsArg(args []string, want string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, want) })
}
