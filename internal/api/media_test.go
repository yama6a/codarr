package api_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/store"
)

var errUnreadable = errors.New("input/output error")

const probeJSON = `{
  "format": {"format_name": "matroska,webm", "duration": "5400.0", "size": "8000000000"},
  "streams": [
    {"index":0,"codec_type":"video","codec_name":"h264","profile":"High","level":41,
     "width":1920,"height":1080,"pix_fmt":"yuv420p","r_frame_rate":"24000/1001","field_order":"progressive"},
    {"index":1,"codec_type":"audio","codec_name":"dts","channels":6,"channel_layout":"5.1",
     "tags":{"language":"eng","title":"Surround"},"disposition":{"default":1}},
    {"index":2,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle",
     "tags":{"language":"eng"},"disposition":{"forced":1}}
  ]
}`

func mediaFixture() domain.MediaFile {
	analysed := testNow.Add(-time.Hour)

	return domain.MediaFile{
		ID:              7,
		Path:            "/media/movies/Dune/Dune.mkv",
		SizeBytes:       8_000_000_000,
		MTime:           1_700_000_000,
		NLink:           1,
		Fingerprint:     "xxh3-128:abc",
		FingerprintAlgo: "xxh3-128",
		ProbeJSON:       probeJSON,
		AnalyzedAt:      &analysed,
		PlanKind:        domain.KindFull,
		PlanReasons:     []string{"video: ENCODE - h264 High"},
		Container:       "matroska",
		VideoCodec:      "h264",
		VideoProfile:    "High",
		VideoLevel:      "4.1",
		VideoBitrate:    11_000_000,
		VideoBitrateSrc: domain.BitrateFromFormat,
		Provenance:      domain.ProvenanceUntouched,
		Status:          domain.MediaAnalyzed,
	}
}

func TestGetMediaFile_RendersTheProbeAsMediaInfo(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)
	noJobs(h.store)

	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) {
		return mediaFixture(), nil
	}

	got := decodeInto[gen.MediaDetail](t, h.do(t, "GET", "/api/media/7", nil), 200)

	require.Equal(t, "Dune.mkv", got.Filename)
	require.Equal(t, "/media/movies/Dune/Dune.mkv", got.Path)
	require.Equal(t, gen.ProvenanceUntouched, got.Provenance)
	require.NotNil(t, got.VideoBitrateKbps)
	require.Equal(t, 11_000, *got.VideoBitrateKbps)

	require.NotNil(t, got.MediaInfo)
	require.NotNil(t, got.MediaInfo.Video)
	require.Equal(t, "h264", got.MediaInfo.Video.Codec)
	require.Equal(t, 1920, got.MediaInfo.Video.Width)
	require.Equal(t, gen.ScanTypeProgressive, got.MediaInfo.Video.Scan)
	require.InDelta(t, 23.976, got.MediaInfo.Video.Fps, 0.01)

	require.Len(t, got.MediaInfo.Audio, 1)
	require.Equal(t, "dts", got.MediaInfo.Audio[0].Codec)
	require.Equal(t, 6, got.MediaInfo.Audio[0].Channels)
	require.Equal(t, "eng", got.MediaInfo.Audio[0].Language)

	require.Len(t, got.Subtitles, 1)
	require.Equal(t, "hdmv_pgs_subtitle", got.Subtitles[0].Codec)
	require.True(t, got.Subtitles[0].Forced)

	require.NotNil(t, got.Width)
	require.Equal(t, 1920, *got.Width)
	require.NotNil(t, got.ProbeJson)
}

func TestListMedia_MapsFilterSortAndPagination(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)

	var seen store.MediaFilter

	h.store.ListMediaFilesFunc = func(_ context.Context, f store.MediaFilter) ([]domain.MediaFile, int, error) {
		seen = f

		return []domain.MediaFile{mediaFixture()}, 137, nil
	}

	got := decodeInto[gen.MediaPage](t, h.do(t, "GET",
		"/api/media?q=dune&status=analyzed&plan_kind=full&video_codec=h264&sort=-size_bytes&page=3&page_size=25",
		nil), 200)

	require.Equal(t, "dune", seen.Query)
	require.Equal(t, []domain.MediaStatus{domain.MediaAnalyzed}, seen.Status)
	require.Equal(t, []domain.Kind{domain.KindFull}, seen.PlanKind)
	require.Equal(t, []string{"h264"}, seen.VideoCodec)
	require.Equal(t, store.SortSize, seen.Sort)
	require.True(t, seen.Descending)
	require.Equal(t, 25, seen.Limit)
	require.Equal(t, 50, seen.Offset)

	require.Equal(t, 137, got.Total)
	require.Equal(t, 3, got.Page)
	require.Equal(t, 25, got.PageSize)
	require.Len(t, got.Items, 1)
	require.Equal(t, "Dune.mkv", got.Items[0].Filename)
	require.Len(t, got.Items[0].Audio, 1)
}

func TestListMedia_CapsThePageSize(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)

	var seen store.MediaFilter

	h.store.ListMediaFilesFunc = func(_ context.Context, f store.MediaFilter) ([]domain.MediaFile, int, error) {
		seen = f

		return nil, 0, nil
	}

	require.Equal(t, 200, h.do(t, "GET", "/api/media?page_size=100000", nil).Code)
	require.Equal(t, 500, seen.Limit)
}

func TestQueueMediaFile_ReportsANoOpRatherThanFailing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue.EnqueueFunc = func(_ context.Context, id int64, origin domain.JobOrigin) (job.EnqueueResult, error) {
		require.Equal(t, int64(7), id)
		require.Equal(t, domain.OriginManual, origin)

		return job.EnqueueResult{
			MediaFileID: 7,
			Enqueued:    false,
			PlanKind:    domain.KindSkip,
			Reason:      "every stream is already compatible",
		}, nil
	}

	got := decodeInto[gen.EnqueueResult](t, h.do(t, "POST", "/api/media/7/queue", nil), 200)

	require.False(t, got.Enqueued)
	require.Equal(t, "every stream is already compatible", got.Reason)
	require.NotNil(t, got.PlanKind)
	require.Equal(t, gen.PlanKindSkip, *got.PlanKind)
}

func TestIgnoreAndUnignoreMediaFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)
	noJobs(h.store)

	media := mediaFixture()
	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return media, nil }
	h.store.SetMediaIgnoredFunc = func(_ context.Context, _ int64, ignored bool) error {
		media.Ignored = ignored

		return nil
	}

	require.True(t, decodeInto[gen.MediaDetail](t, h.do(t, "POST", "/api/media/7/ignore", nil), 200).Ignored)
	require.False(t, decodeInto[gen.MediaDetail](t, h.do(t, "DELETE", "/api/media/7/ignore", nil), 200).Ignored)
}

func TestGetMediaFile_MissingRowIs404(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) {
		return domain.MediaFile{}, store.ErrNotFound
	}

	body := decodeInto[gen.Error](t, h.do(t, "GET", "/api/media/999", nil), 404)
	require.Equal(t, "not_found", body.Error)
}

// plan.md 12: provenance is derived from the fingerprints, never supplied, and
// the whole-file hash is only recomputed when one was recorded at promotion.
func TestVerifyMediaIntegrity_DetectsARewrittenFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")

	media := mediaFixture()
	media.CodarrOutputFingerprint = "xxh3-128:recorded"
	media.CodarrOutputSize = 4_000_000_000
	media.Provenance = domain.ProvenanceCodarrOutput

	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return media, nil }
	h.store.SetMediaIntegrityFunc = func(context.Context, int64, string, string, time.Time) error { return nil }
	h.fs.StatFunc = func(string) (fsx.FileInfo, error) { return fsx.FileInfo{Size: 4_100_000_000}, nil }
	h.fp.SparseFunc = func(string) (string, error) { return "xxh3-128:different", nil }

	got := decodeInto[gen.IntegrityResult](t, h.do(t, "POST", "/api/media/7/verify-integrity", nil), 200)

	require.False(t, got.Ok)
	require.Equal(t, gen.ProvenanceModifiedSinceTranscode, got.Provenance)
	require.False(t, got.FullHashChecked)
	require.Empty(t, h.fp.FullCalls())
	require.NotNil(t, got.CurrentFingerprint)
	require.Equal(t, "xxh3-128:different", *got.CurrentFingerprint)
	require.NotNil(t, got.RecordedFingerprint)
	require.Equal(t, "xxh3-128:recorded", *got.RecordedFingerprint)
}

func TestVerifyMediaIntegrity_ChecksTheFullHashWhenOneWasRecorded(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")

	media := mediaFixture()
	media.CodarrOutputFingerprint = "xxh3-128:recorded"
	media.CodarrOutputFullHash = "xxh3-128:full-recorded"

	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return media, nil }
	h.store.SetMediaIntegrityFunc = func(context.Context, int64, string, string, time.Time) error { return nil }
	h.fs.StatFunc = func(string) (fsx.FileInfo, error) { return fsx.FileInfo{Size: 1}, nil }
	h.fp.SparseFunc = func(string) (string, error) { return "xxh3-128:recorded", nil }
	h.fp.FullFunc = func(string) (string, error) { return "xxh3-128:full-recorded", nil }

	got := decodeInto[gen.IntegrityResult](t, h.do(t, "POST", "/api/media/7/verify-integrity", nil), 200)

	require.True(t, got.Ok)
	require.True(t, got.FullHashChecked)
	require.Equal(t, gen.ProvenanceCodarrOutput, got.Provenance)
	require.Len(t, h.fp.FullCalls(), 1)
}

// A file Codarr never wrote reports ok with provenance untouched.
func TestVerifyMediaIntegrity_UntouchedFileIsOk(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")

	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return mediaFixture(), nil }
	h.store.SetMediaIntegrityFunc = func(context.Context, int64, string, string, time.Time) error { return nil }
	h.fs.StatFunc = func(string) (fsx.FileInfo, error) { return fsx.FileInfo{Size: 1}, nil }
	h.fp.SparseFunc = func(string) (string, error) { return "xxh3-128:whatever", nil }

	got := decodeInto[gen.IntegrityResult](t, h.do(t, "POST", "/api/media/7/verify-integrity", nil), 200)

	require.True(t, got.Ok)
	require.Equal(t, gen.ProvenanceUntouched, got.Provenance)
}

// One unreadable file does not fail the batch.
func TestVerifyMediaIntegrityBulk_UnreadableFileIsReportedNotFatal(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")

	h.store.GetMediaFileFunc = func(_ context.Context, id int64) (domain.MediaFile, error) {
		m := mediaFixture()
		m.ID = id

		return m, nil
	}
	h.store.SetMediaIntegrityFunc = func(context.Context, int64, string, string, time.Time) error { return nil }
	h.fs.StatFunc = func(string) (fsx.FileInfo, error) { return fsx.FileInfo{Size: 1}, nil }
	h.fp.SparseFunc = func(string) (string, error) { return "", errUnreadable }

	got := decodeInto[gen.IntegrityBulkResult](t, h.do(t, "POST", "/api/media/verify-integrity",
		gen.VerifyIntegrityRequest{Ids: []int64{1, 2}}), 200)

	require.Equal(t, 2, got.Checked)
	require.Equal(t, 2, got.Mismatched)
	require.Len(t, got.Results, 2)
	require.NotNil(t, got.Results[0].Message)
	require.Contains(t, *got.Results[0].Message, "fingerprint could not be computed")
}

func TestVerifyMediaIntegrityBulk_EmptyBodyChecksNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	got := decodeInto[gen.IntegrityBulkResult](t, h.do(t, "POST", "/api/media/verify-integrity",
		gen.VerifyIntegrityRequest{}), 200)

	require.Equal(t, 0, got.Checked)
	require.Empty(t, got.Results)
	require.Empty(t, h.store.GetMediaFileCalls())
}

func TestScanRoot_Answers202AndRunsInTheBackground(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	done := make(chan struct{})

	h.store.GetRootFunc = func(_ context.Context, id int64) (domain.Root, error) {
		return domain.Root{ID: id, Path: "/media/movies", Enabled: true}, nil
	}
	h.scanner.ScanRootFunc = func(context.Context, int64) (ingestReport, error) {
		close(done)

		return ingestReport{Roots: 1, Walked: 10}, nil
	}

	got := decodeInto[gen.ScanStarted](t, h.do(t, "POST", "/api/roots/4/scan", nil), 202)

	require.Equal(t, int64(4), got.RootId)
	require.Equal(t, testNow, got.StartedAt.UTC())

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the scan never started")
	}
}

// The detail modal renders the plan for a queued item, so the stream decisions
// have to survive the mapping (plan.md 18.3).
func TestGetMediaFile_RendersThePlanStreams(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)
	noJobs(h.store)

	media := mediaFixture()
	out0, out1 := 0, 1
	media.Plan = &domain.Plan{
		Kind:            domain.KindFull,
		SourceContainer: "matroska",
		OutputContainer: domain.ContainerMatroska,
		PolicyHash:      "914f0f87",
		HDR:             false,
		Reasons:         []string{"plan: FULL"},
		Streams: []domain.StreamPlan{
			{Type: domain.StreamVideo, SourceIndex: 0, OutputIndex: &out0, Decision: domain.DecisionEncode, Reason: "h264 High"},
			{
				Type: domain.StreamAudio, SourceIndex: 0, OutputIndex: &out1, Decision: domain.DecisionEncode,
				Reason: "dts is not on the copy list", Language: "eng", TargetCodec: "ac3",
				TargetBitrate: 640_000, TargetChannels: 6, Default: true,
			},
			{Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionDrop, Reason: "image subtitle", Forced: true},
		},
	}

	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return media, nil }

	got := decodeInto[gen.MediaDetail](t, h.do(t, "GET", "/api/media/7", nil), 200)

	require.NotNil(t, got.Plan)
	require.Equal(t, gen.PlanKindFull, got.Plan.Kind)
	require.Equal(t, gen.ContainerFamilyMatroska, got.Plan.OutputContainer)
	require.Len(t, got.Plan.Streams, 3)

	audio := got.Plan.Streams[1]
	require.Equal(t, gen.StreamTypeAudio, audio.Type)
	require.Equal(t, gen.DecisionEncode, audio.Decision)
	require.NotNil(t, audio.TargetCodec)
	require.Equal(t, "ac3", *audio.TargetCodec)
	require.NotNil(t, audio.TargetBitrateBps)
	require.Equal(t, 640_000, *audio.TargetBitrateBps)
	require.NotNil(t, audio.OutputIndex)
	require.Equal(t, 1, *audio.OutputIndex)

	require.Nil(t, got.Plan.Streams[2].OutputIndex, "a dropped stream has no output position")
	require.True(t, got.Plan.Streams[2].Forced)
}

// TestListMedia_SortsByProvenance closes the gap where the spec offered
// provenance and -provenance but the store had no such column, so the query
// quietly ordered by path instead (plan.md 18.2).
func TestListMedia_SortsByProvenance(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		query      string
		descending bool
	}{
		{query: "provenance", descending: false},
		{query: "-provenance", descending: true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			noInstances(h.store)

			var seen store.MediaFilter

			h.store.ListMediaFilesFunc = func(_ context.Context, f store.MediaFilter) ([]domain.MediaFile, int, error) {
				seen = f

				return []domain.MediaFile{mediaFixture()}, 1, nil
			}

			require.Equal(t, 200, h.do(t, "GET", "/api/media?sort="+tc.query, nil).Code)
			require.Equal(t, store.SortProvenance, seen.Sort)
			require.Equal(t, tc.descending, seen.Descending)
		})
	}
}
