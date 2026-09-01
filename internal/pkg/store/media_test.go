package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/pkg/store/storetest"
)

func TestMediaStore_UpsertIsKeyedOnPath(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	first := seedMedia(t, s, "/library/movies/upsert.mkv")

	second, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
		Path:        "/library/movies/upsert.mkv",
		SizeBytes:   1234,
		MTime:       1_700_000_000,
		NLink:       1,
		Fingerprint: "xxh3-128:changed",
		Status:      domain.MediaNew,
		CreatedAt:   testTime(),
		UpdatedAt:   testTime(),
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, int64(1234), second.SizeBytes)
	require.Equal(t, "xxh3-128:changed", second.Fingerprint)
}

// TestMediaStore_UpsertDerivesProvenance: plan.md 12 makes provenance a
// function of the recorded output fingerprint and the current one. A caller
// cannot set it, and an untouched file is exactly one whose
// codarr_output_fingerprint is NULL.
func TestMediaStore_UpsertDerivesProvenance(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	m, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
		Path:        "/library/movies/provenance.mkv",
		SizeBytes:   10,
		MTime:       1,
		Fingerprint: "xxh3-128:source",
		Status:      domain.MediaNew,
		Provenance:  domain.ProvenanceCodarrOutput, // ignored on purpose
		CreatedAt:   testTime(),
		UpdatedAt:   testTime(),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ProvenanceUntouched, m.Provenance)
}

// TestMediaStore_RecordPromotionLeavesTheNextScanNothingToDo is loop
// prevention (plan.md 12 and step 9 of 15.2): the current size, mtime and
// fingerprint become the output's own, so a rescan sees an unchanged file
// rather than re-probing Codarr's own work.
func TestMediaStore_RecordPromotionLeavesTheNextScanNothingToDo(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/promoted.mkv")
	job := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	const (
		outputFingerprint = "xxh3-128:output"
		outputSize        = int64(8_102_938_475)
		outputMTime       = int64(1_735_689_600)
		policyHash        = "a41f9c22"
	)

	require.NoError(t, s.RecordPromotion(t.Context(), store.PromotionUpdate{
		JobID:             job.ID,
		MediaFileID:       media.ID,
		OutputFingerprint: outputFingerprint,
		OutputFullHash:    "xxh3-128:full",
		OutputSize:        outputSize,
		OutputMTime:       outputMTime,
		PolicyHash:        policyHash,
		Transform:         domain.TransformRecord{Size: domain.SizeTransform{AfterBytes: outputSize}},
		ActualSeconds:     218,
		PromotedAt:        testTime(),
	}))

	promoted, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, outputSize, promoted.SizeBytes)
	require.Equal(t, outputMTime, promoted.MTime)
	require.Equal(t, outputFingerprint, promoted.Fingerprint)
	require.Equal(t, outputFingerprint, promoted.CodarrOutputFingerprint)
	require.Equal(t, outputSize, promoted.CodarrOutputSize)
	require.Equal(t, outputMTime, promoted.CodarrOutputMTime)
	require.Equal(t, "xxh3-128:full", promoted.CodarrOutputFullHash)
	require.Equal(t, policyHash, promoted.CodarrPolicyHash)
	require.True(t, promoted.CodarrTagged)
	require.Equal(t, domain.ProvenanceCodarrOutput, promoted.Provenance)
	require.Equal(t, domain.MediaDone, promoted.Status)
	require.NotNil(t, promoted.CodarrJobID)
	require.Equal(t, job.ID, *promoted.CodarrJobID)

	done, err := s.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobDone, done.State)
	require.Equal(t, outputSize, done.OutputSize)
	require.Equal(t, outputFingerprint, done.OutputFingerprint)
	require.Equal(t, 218, done.ActualSeconds)
	require.NotNil(t, done.FinishedAt)
}

// TestMediaStore_ProvenanceGoesModifiedWhenSomethingRewritesTheFile is the
// Bazarr subtitle embed of plan.md 12: the CODARR tag survives, the
// fingerprint does not.
func TestMediaStore_ProvenanceGoesModifiedWhenSomethingRewritesTheFile(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/bazarr.mkv")
	job := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	require.NoError(t, s.RecordPromotion(t.Context(), store.PromotionUpdate{
		JobID:             job.ID,
		MediaFileID:       media.ID,
		OutputFingerprint: "xxh3-128:output",
		OutputSize:        100,
		OutputMTime:       1,
		PolicyHash:        "a41f9c22",
		PromotedAt:        testTime(),
	}))

	rescanned, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
		Path:        "/library/movies/bazarr.mkv",
		SizeBytes:   120,
		MTime:       2,
		Fingerprint: "xxh3-128:rewritten",
		Status:      domain.MediaNew,
		CreatedAt:   testTime(),
		UpdatedAt:   testTime(),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ProvenanceModified, rescanned.Provenance)

	unchanged, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
		Path:        "/library/movies/bazarr.mkv",
		SizeBytes:   100,
		MTime:       1,
		Fingerprint: "xxh3-128:output",
		Status:      domain.MediaDone,
		CreatedAt:   testTime(),
		UpdatedAt:   testTime(),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ProvenanceCodarrOutput, unchanged.Provenance)
}

func TestMediaStore_AnalysisRecomputesProvenanceAndStoresThePlan(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/analyzed.mkv")

	plan := &domain.Plan{
		Kind:            domain.KindAudioOnly,
		SourceContainer: "matroska",
		OutputContainer: domain.ContainerMatroska,
		Streams: []domain.StreamPlan{
			{Type: domain.StreamVideo, SourceIndex: 0, Decision: domain.DecisionCopy, Reason: "h264 High L4.0"},
		},
		Reasons:    []string{"dts not in copy list for 3+ channels"},
		PolicyHash: "a41f9c22",
	}

	require.NoError(t, s.UpdateMediaAnalysis(t.Context(), store.AnalysisUpdate{
		MediaFileID:     media.ID,
		SizeBytes:       9_871_234_567,
		MTime:           1_735_689_600,
		NLink:           1,
		Fingerprint:     "xxh3-128:source",
		FingerprintAlgo: "xxh3-128",
		ProbeJSON:       `{"format":{}}`,
		MediaInfoJSON:   `{"video":"h264"}`,
		Plan:            plan,
		PlanKind:        domain.KindAudioOnly,
		PlanReasons:     []string{"dts not in copy list for 3+ channels"},
		Container:       "matroska",
		VideoCodec:      "h264",
		VideoProfile:    "High",
		VideoLevel:      "4.0",
		VideoBitrate:    8_420_000,
		VideoBitrateSrc: domain.BitrateFromBPSTag,
		CodarrTagged:    false,
		Status:          domain.MediaAnalyzed,
		AnalyzedAt:      testTime(),
	}))

	analyzed, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, plan, analyzed.Plan)
	require.Equal(t, domain.KindAudioOnly, analyzed.PlanKind)
	require.Equal(t, []string{"dts not in copy list for 3+ channels"}, analyzed.PlanReasons)
	require.Equal(t, "h264", analyzed.VideoCodec)
	require.Equal(t, 8_420_000, analyzed.VideoBitrate)
	require.Equal(t, domain.BitrateFromBPSTag, analyzed.VideoBitrateSrc)
	require.Equal(t, domain.ProvenanceUntouched, analyzed.Provenance)
	require.Equal(t, domain.MediaAnalyzed, analyzed.Status)
	require.NotNil(t, analyzed.AnalyzedAt)
	require.Equal(t, testTime(), *analyzed.AnalyzedAt)
}

func TestMediaStore_ListFiltersSortsAndPaginates(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	instance, err := s.CreateArrInstance(t.Context(), domain.ArrInstance{
		Name: "radarr-4k", Flavour: domain.FlavourRadarr, BaseURL: "http://radarr:7878",
		APIKey: "key", WebhookID: "hook", Enabled: true,
		CreatedAt: testTime(), UpdatedAt: testTime(),
	})
	require.NoError(t, err)

	for _, spec := range []struct {
		path   string
		codec  string
		kind   domain.Kind
		status domain.MediaStatus
	}{
		{"/library/movies/alpha.mkv", "h264", domain.KindFull, domain.MediaAnalyzed},
		{"/library/movies/bravo.mkv", "hevc", domain.KindSkip, domain.MediaDone},
		{"/library/shows/charlie.mkv", "h264", domain.KindAudioOnly, domain.MediaAnalyzed},
	} {
		m, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
			Path: spec.path, SizeBytes: 1, MTime: 1, ArrInstanceID: &instance.ID,
			Status: spec.status, CreatedAt: testTime(), UpdatedAt: testTime(),
		})
		require.NoError(t, err)

		require.NoError(t, s.UpdateMediaAnalysis(t.Context(), store.AnalysisUpdate{
			MediaFileID: m.ID, SizeBytes: 1, MTime: 1, VideoCodec: spec.codec,
			PlanKind: spec.kind, Status: spec.status, AnalyzedAt: testTime(),
		}))
	}

	all, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{Sort: store.SortPath})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, all, 3)
	require.Equal(t, "/library/movies/alpha.mkv", all[0].Path)

	byCodec, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{VideoCodec: []string{"h264"}})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, byCodec, 2)

	byKind, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{
		PlanKind: []domain.Kind{domain.KindAudioOnly},
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "/library/shows/charlie.mkv", byKind[0].Path)

	byQuery, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{Query: "/shows/"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "/library/shows/charlie.mkv", byQuery[0].Path)

	byStatus, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{
		Status: []domain.MediaStatus{domain.MediaDone},
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "/library/movies/bravo.mkv", byStatus[0].Path)

	byInstance, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{ArrInstanceID: &instance.ID})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, byInstance, 3)

	byProvenance, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{
		Provenance: []domain.Provenance{domain.ProvenanceUntouched},
	})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, byProvenance, 3)

	// The total is the count under the filter, not the page.
	page, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{
		Sort: store.SortPath, Descending: true, Limit: 1, Offset: 1,
	})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, page, 1)
	require.Equal(t, "/library/movies/bravo.mkv", page[0].Path)
}

// TestMediaStore_ListExcludesIgnoredUnlessAsked keeps the per-file ignore list
// of plan.md 13.3 out of the default library view.
func TestMediaStore_ListExcludesIgnoredUnlessAsked(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/ignored.mkv")

	require.NoError(t, s.SetMediaIgnored(t.Context(), media.ID, true))

	visible, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{})
	require.NoError(t, err)
	require.Equal(t, 0, total)
	require.Empty(t, visible)

	withIgnored, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{IncludeIgnored: true})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, withIgnored, 1)
}

// TestMediaStore_ListQueryEscapesLikeWildcards stops a path fragment containing
// % from matching everything.
func TestMediaStore_ListQueryEscapesLikeWildcards(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	seedMedia(t, s, "/library/movies/plain.mkv")
	seedMedia(t, s, "/library/movies/100%.mkv")

	hits, total, err := s.ListMediaFiles(t.Context(), store.MediaFilter{Query: "100%"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "/library/movies/100%.mkv", hits[0].Path)
}

// TestMediaStore_MarkMissingKeepsTheRow is the prune rule of plan.md 13.2: the
// row and its history survive, only the status changes.
func TestMediaStore_MarkMissingKeepsTheRow(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/gone.mkv")

	n, err := s.MarkMediaMissing(t.Context(), []int64{media.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	still, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MediaMissing, still.Status)

	n, err = s.MarkMediaMissing(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestMediaStore_ListStatsByRoot(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	root, err := s.CreateRoot(t.Context(), domain.Root{
		Path: "/library/movies", Enabled: true, CreatedAt: testTime(),
	})
	require.NoError(t, err)

	m, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
		Path: "/library/movies/rooted.mkv", RootID: &root.ID, SizeBytes: 42, MTime: 7,
		Status: domain.MediaNew, CreatedAt: testTime(), UpdatedAt: testTime(),
	})
	require.NoError(t, err)

	stats, err := s.ListMediaStatsByRoot(t.Context(), root.ID)
	require.NoError(t, err)
	require.Equal(t, []store.MediaStat{{
		ID:        m.ID,
		Path:      "/library/movies/rooted.mkv",
		SizeBytes: 42,
		MTime:     7,
		Status:    domain.MediaNew,
		Ignored:   false,
	}}, stats)
}

func TestMediaStore_SetIntegrityRederivesProvenance(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/integrity.mkv")
	job := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	require.NoError(t, s.RecordPromotion(t.Context(), store.PromotionUpdate{
		JobID: job.ID, MediaFileID: media.ID, OutputFingerprint: "xxh3-128:output",
		OutputSize: 100, OutputMTime: 1, PolicyHash: "a41f9c22", PromotedAt: testTime(),
	}))

	require.NoError(t, s.SetMediaIntegrity(t.Context(), media.ID, "xxh3-128:drifted", "", testTime()))

	checked, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProvenanceModified, checked.Provenance)
	require.NotNil(t, checked.IntegrityCheckedAt)
	require.Equal(t, testTime(), *checked.IntegrityCheckedAt)
}

func TestMediaStore_GetNotFound(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	_, err := s.GetMediaFile(t.Context(), 404)
	require.ErrorIs(t, err, store.ErrNotFound)

	_, err = s.GetMediaFileByPath(t.Context(), "/nope.mkv")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestMediaStore_CountsForTheDashboard(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	for _, path := range []string{"/library/one.mkv", "/library/two.mkv"} {
		m := seedMedia(t, s, path)
		require.NoError(t, s.UpdateMediaAnalysis(t.Context(), store.AnalysisUpdate{
			MediaFileID: m.ID, SizeBytes: 1, MTime: 1, PlanKind: domain.KindFull,
			Status: domain.MediaAnalyzed, AnalyzedAt: testTime(),
		}))
	}

	byStatus, err := s.CountMediaByStatus(t.Context())
	require.NoError(t, err)
	require.Equal(t, map[domain.MediaStatus]int{domain.MediaAnalyzed: 2}, byStatus)

	byKind, err := s.CountMediaByPlanKind(t.Context())
	require.NoError(t, err)
	require.Equal(t, map[domain.Kind]int{domain.KindFull: 2}, byKind)
}
