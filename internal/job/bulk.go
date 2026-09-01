package job

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// The space reclaim sweep of plan.md 11. It never runs automatically and is
// never triggered by ingest; only an explicit, confirmed request reaches it.
const (
	// SweepVideoCodec is the only codec worth re-encoding for space. Everything
	// else in a normal library is either already HEVC or too small to matter.
	SweepVideoCodec = "h264"

	// SweepMinBitrate is the floor a file has to clear to be sample-probed at
	// all, in bits per second, resolved per 8.4.
	SweepMinBitrate = 8_000_000

	// SweepMinSavingPct is the projected saving a file has to beat to be
	// queued. Evaluated against the probe result rather than a fixed table, so
	// it spares files that are large because the content is complex.
	SweepMinSavingPct = 35.0
)

// mediaPageSize is how many rows a bulk operation pulls at a time. The library
// is tens of thousands of files and none of these operations needs it in memory
// all at once.
const mediaPageSize = 500

// PlanKindBreakdown is the count per plan kind every bulk preview reports, so
// the confirmation can say exactly what it is about to do (19).
type PlanKindBreakdown struct {
	Skip      int
	Remux     int
	AudioOnly int
	Full      int
}

func (b *PlanKindBreakdown) add(kind domain.Kind) {
	switch kind {
	case domain.KindSkip:
		b.Skip++
	case domain.KindRemux:
		b.Remux++
	case domain.KindAudioOnly:
		b.AudioOnly++
	case domain.KindFull:
		b.Full++
	}
}

// Recheck is one re-check request: a selection, a filter, or neither. An empty
// request selects nothing rather than everything, so a mis-sent body cannot
// queue the library.
type Recheck struct {
	IDs     []int64
	Filter  *store.MediaFilter
	Confirm bool
}

// RecheckResult is the preview, and the same shape after confirmation.
type RecheckResult struct {
	DryRun       bool
	Examined     int
	Count        int
	ByPlanKind   PlanKindBreakdown
	MediaFileIDs []int64
	QueuedJobIDs []int64

	// Irreversible is always true. plan.md 15.5: the confirmation has to say so
	// plainly, because promotion destroys the original.
	Irreversible bool
}

// RecheckAll re-probes every done file, re-plans it against the current policy
// and queues whatever no longer matches (19). Nothing is queued unless confirm
// is set.
func (s *Service) RecheckAll(ctx context.Context, confirm bool) (RecheckResult, error) {
	return s.Recheck(ctx, Recheck{
		Filter:  &store.MediaFilter{Status: []domain.MediaStatus{domain.MediaDone}},
		Confirm: confirm,
	})
}

// Recheck is the same operation restricted to a selection or a filter. It
// re-probes first and skips anything already correct, so selecting everything
// is safe.
func (s *Service) Recheck(ctx context.Context, req Recheck) (RecheckResult, error) {
	files, err := s.selectMedia(ctx, req.IDs, req.Filter)
	if err != nil {
		return RecheckResult{}, err
	}

	out := RecheckResult{DryRun: !req.Confirm, Irreversible: true}

	for _, m := range files {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("re-check cancelled: %w", err)
		}

		refreshed, err := s.analyzer.Analyze(ctx, m)
		if err != nil {
			s.log.WarnContext(ctx, "re-analysing a file failed, skipping it",
				slog.Int64("media_file_id", m.ID), slog.String("path", m.Path), slog.Any("error", err))

			continue
		}

		out.Examined++

		if err := s.considerRecheck(ctx, refreshed, req.Confirm, &out); err != nil {
			return out, err
		}
	}

	return out, nil
}

func (s *Service) considerRecheck(ctx context.Context, m domain.MediaFile, confirm bool, out *RecheckResult) error {
	if _, ok := blockedFromQueue(m); !ok {
		return nil
	}

	out.Count++
	out.ByPlanKind.add(m.PlanKind)
	out.MediaFileIDs = append(out.MediaFileIDs, m.ID)

	if !confirm {
		return nil
	}

	res, err := s.enqueue(ctx, m, domain.OriginRecheck)
	if err != nil {
		return err
	}

	if res.Enqueued && res.JobID != nil {
		out.QueuedJobIDs = append(out.QueuedJobIDs, *res.JobID)
	}

	return nil
}

// SpaceSweepCandidate is one file the sweep would re-encode, with the numbers
// the confirmation shows.
type SpaceSweepCandidate struct {
	MediaFileID             int64
	Path                    string
	Filename                string
	VideoCodec              string
	CurrentVideoBitrateKbps int
	TargetVideoBitrateKbps  int
	CurrentBytes            int64
	ProjectedBytes          int64
	ProjectedSavingBytes    int64
	ProjectedSavingPct      float64
}

// SpaceSweepPreview is the dry run of plan.md 11.
type SpaceSweepPreview struct {
	Count                int
	Examined             int
	ByPlanKind           PlanKindBreakdown
	CurrentBytes         int64
	ProjectedBytes       int64
	ProjectedSavingBytes int64
	ProjectedSavingPct   float64
	Irreversible         bool
	Candidates           []SpaceSweepCandidate
	QueuedJobIDs         []int64
}

// SpaceSweepPreview finds H.264 files above the bitrate floor, sample-probes
// each one and keeps only those whose projected saving clears the threshold.
// It queues nothing.
func (s *Service) SpaceSweepPreview(ctx context.Context) (SpaceSweepPreview, error) {
	return s.spaceSweep(ctx, nil, false)
}

// SpaceSweepRun queues the sweep. confirm must be set: the operation replaces
// every file it touches and there is no undo (15.5). Passing ids runs exactly
// what a preview showed, and each one is re-evaluated rather than trusted, so a
// stale preview cannot queue a file that no longer clears the threshold.
func (s *Service) SpaceSweepRun(ctx context.Context, ids []int64, confirm bool) (SpaceSweepPreview, error) {
	if !confirm {
		return SpaceSweepPreview{}, ErrConfirmationRequired
	}

	return s.spaceSweep(ctx, ids, true)
}

func (s *Service) spaceSweep(ctx context.Context, ids []int64, queue bool) (SpaceSweepPreview, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return SpaceSweepPreview{}, fmt.Errorf("reading the settings: %w", err)
	}

	files, err := s.sweepCandidates(ctx, ids)
	if err != nil {
		return SpaceSweepPreview{}, err
	}

	out := SpaceSweepPreview{Irreversible: true}

	for _, m := range files {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("space sweep cancelled: %w", err)
		}

		out.Examined++

		candidate, ok := s.evaluate(ctx, settings, m)
		if !ok {
			continue
		}

		out.Count++
		out.ByPlanKind.add(domain.KindFull)
		out.Candidates = append(out.Candidates, candidate)
		out.CurrentBytes += candidate.CurrentBytes
		out.ProjectedBytes += candidate.ProjectedBytes
		out.ProjectedSavingBytes += candidate.ProjectedSavingBytes

		if err := s.queueSweep(ctx, m, queue, &out); err != nil {
			return out, err
		}
	}

	if out.CurrentBytes > 0 {
		out.ProjectedSavingPct = float64(out.ProjectedSavingBytes) / float64(out.CurrentBytes) * 100
	}

	return out, nil
}

func (s *Service) queueSweep(ctx context.Context, m domain.MediaFile, queue bool, out *SpaceSweepPreview) error {
	if !queue {
		return nil
	}

	res, err := s.enqueue(ctx, m, domain.OriginSpaceSweep)
	if err != nil {
		return err
	}

	if res.Enqueued && res.JobID != nil {
		out.QueuedJobIDs = append(out.QueuedJobIDs, *res.JobID)
	}

	return nil
}

// evaluate is the per-file half of plan.md 11: sample-probe the content, work
// out what HEVC would cost for it, and keep the file only if the projected
// saving beats the threshold. A file it cannot measure is not a candidate.
func (s *Service) evaluate(ctx context.Context, settings domain.Settings, m domain.MediaFile) (SpaceSweepCandidate, bool) {
	probe, err := storedProbe(m)
	if err != nil {
		s.log.WarnContext(ctx, "the stored probe could not be read, skipping the file",
			slog.Int64("media_file_id", m.ID), slog.Any("error", err))

		return SpaceSweepCandidate{}, false
	}

	in := bitrateInput(probe)
	duration := probe.Duration()

	if in.SourceBitrate <= 0 || duration <= 0 {
		return SpaceSweepCandidate{}, false
	}

	name := "sweep-" + strconv.FormatInt(m.ID, 10)

	base, err := s.sampleProbe(ctx, settings.TempDir, name, m.Path, duration)
	if err != nil {
		s.log.WarnContext(ctx, "the sample probe failed, skipping the file",
			slog.Int64("media_file_id", m.ID), slog.String("path", m.Path), slog.Any("error", err))

		return SpaceSweepCandidate{}, false
	}

	return projection(m, in, ffmpeg.TargetFromSamples([]int{base}, in), duration)
}

// projection turns a measured target into the file-level numbers the
// confirmation shows. The threshold is applied to the projected file size
// rather than to the video bitrate alone, because what the sweep reclaims is
// disk space and the audio it leaves alone still occupies some.
func projection(m domain.MediaFile, in ffmpeg.BitrateInput, target int, duration float64) (SpaceSweepCandidate, bool) {
	if target <= 0 || target >= in.SourceBitrate {
		return SpaceSweepCandidate{}, false
	}

	saved := int64(float64(in.SourceBitrate-target) * duration / 8)
	if saved <= 0 || m.SizeBytes <= 0 {
		return SpaceSweepCandidate{}, false
	}

	saved = min(saved, m.SizeBytes)
	pct := float64(saved) / float64(m.SizeBytes) * 100

	if pct <= SweepMinSavingPct {
		return SpaceSweepCandidate{}, false
	}

	return SpaceSweepCandidate{
		MediaFileID:             m.ID,
		Path:                    m.Path,
		Filename:                filepath.Base(m.Path),
		VideoCodec:              m.VideoCodec,
		CurrentVideoBitrateKbps: in.SourceBitrate / 1000,
		TargetVideoBitrateKbps:  target / 1000,
		CurrentBytes:            m.SizeBytes,
		ProjectedBytes:          m.SizeBytes - saved,
		ProjectedSavingBytes:    saved,
		ProjectedSavingPct:      pct,
	}, true
}

// sweepCandidates is the cheap filter that runs before any sample probe:
// H.264 video above the bitrate floor, resolved per 8.4.
func (s *Service) sweepCandidates(ctx context.Context, ids []int64) ([]domain.MediaFile, error) {
	files, err := s.selectMedia(ctx, ids, &store.MediaFilter{VideoCodec: []string{SweepVideoCodec}})
	if err != nil {
		return nil, err
	}

	out := make([]domain.MediaFile, 0, len(files))

	for _, m := range files {
		if m.Ignored || m.VideoCodec != SweepVideoCodec || m.VideoBitrate <= SweepMinBitrate {
			continue
		}

		out = append(out, m)
	}

	return out, nil
}

// selectMedia resolves the "ids or filter, never both" of the bulk endpoints.
// Neither selects nothing.
func (s *Service) selectMedia(ctx context.Context, ids []int64, filter *store.MediaFilter) ([]domain.MediaFile, error) {
	if len(ids) > 0 {
		out := make([]domain.MediaFile, 0, len(ids))

		for _, id := range ids {
			m, err := s.store.GetMediaFile(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("loading media file %d: %w", id, err)
			}

			out = append(out, m)
		}

		return out, nil
	}

	if filter == nil {
		return nil, nil
	}

	return s.listAll(ctx, *filter)
}

func (s *Service) listAll(ctx context.Context, f store.MediaFilter) ([]domain.MediaFile, error) {
	f.Limit = mediaPageSize

	var out []domain.MediaFile

	for {
		f.Offset = len(out)

		batch, total, err := s.store.ListMediaFiles(ctx, f)
		if err != nil {
			return nil, fmt.Errorf("listing media files: %w", err)
		}

		out = append(out, batch...)

		if len(batch) == 0 || len(out) >= total {
			return out, nil
		}
	}
}
