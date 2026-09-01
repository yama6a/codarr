package api

import (
	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// The detail modal's media summary (plan.md 18.3) and the library table's
// per-track columns (18.2) are both derived from the stored ffprobe output.
// Nothing writes a pre-digested media_info_json today, so it is parsed here
// rather than duplicated into a second column that could disagree with the
// probe it came from.

// probeSummary is everything the API renders out of one stored probe.
type probeSummary struct {
	Info      *gen.MediaInfo
	Audio     []gen.AudioSummary
	Subtitles []gen.SubtitleSummary
}

func summarise(probeJSON string) probeSummary {
	out := probeSummary{Audio: []gen.AudioSummary{}, Subtitles: []gen.SubtitleSummary{}}

	if probeJSON == "" {
		return out
	}

	probe, err := ffprobe.Parse([]byte(probeJSON))
	if err != nil {
		// A row whose probe cannot be parsed still renders; the raw JSON is
		// carried through so the collapsed technical section shows what broke.
		return out
	}

	info := gen.MediaInfo{
		Attachments:     len(probe.StreamsOfType(ffprobe.TypeAttachment)),
		Audio:           []gen.MediaInfoAudio{},
		Chapters:        len(probe.Chapters),
		Container:       probe.Format.FormatName,
		DurationSeconds: floatPtr(probe.Duration()),
		Subtitles:       []gen.MediaInfoSubtitle{},
	}

	if v, ok := probe.PrimaryVideo(); ok {
		info.Video = probeVideoState(v)
	}

	for i, s := range probe.StreamsOfType(ffprobe.TypeAudio) {
		info.Audio = append(info.Audio, probeAudio(i, s))
		out.Audio = append(out.Audio, gen.AudioSummary{
			Channels: s.Channels,
			Codec:    s.CodecName,
			Language: s.Language(),
			Layout:   s.ChannelLayout,
		})
	}

	for i, s := range probe.StreamsOfType(ffprobe.TypeSubtitle) {
		info.Subtitles = append(info.Subtitles, gen.MediaInfoSubtitle{
			Codec:    s.CodecName,
			Default:  s.Disposition.Default == 1,
			Forced:   s.Disposition.Forced == 1,
			Index:    i,
			Language: s.Language(),
			Title:    strPtr(s.Title()),
		})
		out.Subtitles = append(out.Subtitles, gen.SubtitleSummary{
			Codec:    s.CodecName,
			Forced:   s.Disposition.Forced == 1,
			Language: s.Language(),
		})
	}

	out.Info = &info

	return out
}

func probeVideoState(v ffprobe.Stream) *gen.VideoState {
	bitrate, ok := v.BitRateBPS()
	if !ok {
		bitrate, ok = v.BPSTagBPS()
	}

	var kbps *int
	if ok && bitrate > 0 {
		kbps = ptrOf(bitrate / 1000)
	}

	return &gen.VideoState{
		BitrateKbps: kbps,
		Codec:       v.CodecName,
		Fps:         v.FrameRate(),
		Hdr:         v.IsHDR(),
		Height:      v.Height,
		Level:       v.LevelString(),
		PixFmt:      v.PixFmt,
		Profile:     v.Profile,
		Scan:        scanOf(v),
		Width:       v.Width,
	}
}

// scanOf follows plan.md 6.2: an unknown or absent field_order is progressive,
// never interlaced.
func scanOf(v ffprobe.Stream) gen.ScanType {
	if v.Interlaced() {
		return gen.ScanTypeInterlaced
	}

	return gen.ScanTypeProgressive
}

func probeAudio(index int, s ffprobe.Stream) gen.MediaInfoAudio {
	var kbps *int

	if bps, ok := s.BitRateBPS(); ok && bps > 0 {
		kbps = ptrOf(bps / 1000)
	} else if bps, ok := s.BPSTagBPS(); ok && bps > 0 {
		kbps = ptrOf(bps / 1000)
	}

	return gen.MediaInfoAudio{
		BitrateKbps:    kbps,
		Channels:       s.Channels,
		Codec:          s.CodecName,
		Comment:        s.Disposition.Comment == 1,
		Default:        s.Disposition.Default == 1,
		Forced:         s.Disposition.Forced == 1,
		Index:          index,
		Language:       s.Language(),
		Layout:         s.ChannelLayout,
		Profile:        strPtr(s.Profile),
		Title:          strPtr(s.Title()),
		VisualImpaired: s.Disposition.VisualImpaired == 1,
	}
}

func mediaListItem(m domain.MediaFile, instanceName string) gen.MediaListItem {
	sum := summarise(m.ProbeJSON)

	return gen.MediaListItem{
		AnalyzedAt:         m.AnalyzedAt,
		ArrInstanceId:      m.ArrInstanceID,
		ArrInstanceName:    strPtr(instanceName),
		Audio:              sum.Audio,
		CodarrTagged:       m.CodarrTagged,
		Container:          strPtr(m.Container),
		Filename:           filename(m.Path),
		Height:             intPtr(heightOf(sum)),
		Id:                 m.ID,
		Ignored:            m.Ignored,
		IsHdr:              m.IsHDR,
		Path:               m.Path,
		PlanKind:           planKindPtr(m.PlanKind),
		Provenance:         gen.Provenance(m.Provenance),
		RootId:             m.RootID,
		SizeBytes:          m.SizeBytes,
		Status:             gen.MediaStatus(m.Status),
		Subtitles:          sum.Subtitles,
		UpdatedAt:          m.UpdatedAt,
		VideoBitrateKbps:   intPtr(m.VideoBitrate / 1000),
		VideoBitrateSource: bitrateSourcePtr(m.VideoBitrateSrc),
		VideoCodec:         strPtr(m.VideoCodec),
		VideoLevel:         strPtr(m.VideoLevel),
		VideoProfile:       strPtr(m.VideoProfile),
		Width:              intPtr(widthOf(sum)),
	}
}

func mediaDetail(m domain.MediaFile, instanceName string, latestJobID *int64) gen.MediaDetail {
	sum := summarise(m.ProbeJSON)

	return gen.MediaDetail{
		AnalyzedAt:              m.AnalyzedAt,
		ArrEntityId:             m.ArrEntityID,
		ArrInstanceId:           m.ArrInstanceID,
		ArrInstanceName:         strPtr(instanceName),
		Audio:                   sum.Audio,
		CodarrJobId:             m.CodarrJobID,
		CodarrOutputFingerprint: strPtr(m.CodarrOutputFingerprint),
		CodarrOutputFullHash:    strPtr(m.CodarrOutputFullHash),
		CodarrOutputMtime:       int64Ptr(m.CodarrOutputMTime),
		CodarrOutputSize:        int64Ptr(m.CodarrOutputSize),
		CodarrPolicyHash:        strPtr(m.CodarrPolicyHash),
		CodarrProcessedAt:       m.CodarrProcessedAt,
		CodarrTagged:            m.CodarrTagged,
		Container:               strPtr(m.Container),
		CreatedAt:               m.CreatedAt,
		Filename:                filename(m.Path),
		Fingerprint:             strPtr(m.Fingerprint),
		FingerprintAlgo:         strPtr(m.FingerprintAlgo),
		Height:                  intPtr(heightOf(sum)),
		Id:                      m.ID,
		Ignored:                 m.Ignored,
		IntegrityCheckedAt:      m.IntegrityCheckedAt,
		IsHdr:                   m.IsHDR,
		LastError:               strPtr(m.LastError),
		LatestJobId:             latestJobID,
		MediaInfo:               sum.Info,
		Mtime:                   m.MTime,
		Nlink:                   intPtr(m.NLink),
		Path:                    m.Path,
		Plan:                    plan(m.Plan),
		PlanKind:                planKindPtr(m.PlanKind),
		PlanReasons:             nonNilStrings(m.PlanReasons),
		ProbeJson:               strPtr(m.ProbeJSON),
		Provenance:              gen.Provenance(m.Provenance),
		RootId:                  m.RootID,
		SizeBytes:               m.SizeBytes,
		Status:                  gen.MediaStatus(m.Status),
		Subtitles:               sum.Subtitles,
		UpdatedAt:               m.UpdatedAt,
		VideoBitrateKbps:        intPtr(m.VideoBitrate / 1000),
		VideoBitrateSource:      bitrateSourcePtr(m.VideoBitrateSrc),
		VideoCodec:              strPtr(m.VideoCodec),
		VideoLevel:              strPtr(m.VideoLevel),
		VideoProfile:            strPtr(m.VideoProfile),
		Width:                   intPtr(widthOf(sum)),
	}
}

func widthOf(s probeSummary) int {
	if s.Info == nil || s.Info.Video == nil {
		return 0
	}

	return s.Info.Video.Width
}

func heightOf(s probeSummary) int {
	if s.Info == nil || s.Info.Video == nil {
		return 0
	}

	return s.Info.Video.Height
}

func planKindPtr(k domain.Kind) *gen.PlanKind {
	if k == "" {
		return nil
	}

	return ptrOf(gen.PlanKind(k))
}

func bitrateSourcePtr(b domain.BitrateSource) *gen.BitrateSource {
	if b == "" {
		return nil
	}

	return ptrOf(gen.BitrateSource(b))
}
