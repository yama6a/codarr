package api

import (
	"path/filepath"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Mapping between the domain types and the generated wire types. It is all in
// one file so a spec change lands in one place, and it is all pure functions so
// the secret rule (18.4) is enforced in exactly one place per secret.

func ptrOf[T any](v T) *T { return &v }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func intPtr(v int) *int {
	if v == 0 {
		return nil
	}

	return &v
}

func int64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}

	return &v
}

func floatPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}

	return &v
}

func pathMappings(in []domain.PathMapping) []gen.PathMapping {
	out := make([]gen.PathMapping, 0, len(in))
	for _, m := range in {
		out = append(out, gen.PathMapping{
			Id:     int64Ptr(m.ID),
			Local:  m.Local,
			Remote: m.Remote,
			Sort:   m.Sort,
		})
	}

	return out
}

func domainMappings(in []gen.PathMapping) ([]domain.PathMapping, error) {
	out := make([]domain.PathMapping, 0, len(in))

	for i, m := range in {
		local, err := mappingPath(m.Local, "local")
		if err != nil {
			return nil, err
		}

		remote, err := mappingPath(m.Remote, "remote")
		if err != nil {
			return nil, err
		}

		sort := m.Sort
		if sort == 0 {
			sort = i
		}

		out = append(out, domain.PathMapping{Local: local, Remote: remote, Sort: sort})
	}

	return out, nil
}

// arrInstance never carries the API key. plan.md 18.4: reads return the mask.
func arrInstance(a domain.ArrInstance, mappings []domain.PathMapping) gen.ArrInstance {
	return gen.ArrInstance{
		ApiKey:         masked(a.APIKey),
		BaseUrl:        a.BaseURL,
		CreatedAt:      a.CreatedAt,
		Enabled:        a.Enabled,
		Flavour:        gen.Flavour(a.Flavour),
		Id:             a.ID,
		LastTestResult: strPtr(a.LastTestResult),
		LastTestedAt:   a.LastTestedAt,
		Name:           a.Name,
		PathMappings:   pathMappings(mappings),
		RescanAfter:    a.RescanAfter,
		UnmonitorAfter: a.UnmonitorAfter,
		UpdatedAt:      a.UpdatedAt,
		WebhookId:      a.WebhookID,
	}
}

func root(r domain.Root, instanceName string, fileCount *int) gen.Root {
	return gen.Root{
		ArrInstanceId:   r.ArrInstanceID,
		ArrInstanceName: strPtr(instanceName),
		CreatedAt:       r.CreatedAt,
		Enabled:         r.Enabled,
		Id:              r.ID,
		Imported:        r.Imported,
		MediaFileCount:  fileCount,
		Path:            r.Path,
	}
}

// plexConfig never carries the token (18.4).
func plexConfig(c domain.PlexConfig, mappings []domain.PathMapping) gen.PlexConfig {
	return gen.PlexConfig{
		AnalyzeAfter:       c.AnalyzeAfter,
		BaseUrl:            c.BaseURL,
		ClientIdentifier:   c.ClientIdentifier,
		GuardActiveStreams: c.GuardActiveStreams,
		LastTestResult:     strPtr(c.LastTestResult),
		LastTestedAt:       c.LastTestedAt,
		PathMappings:       pathMappings(mappings),
		RefreshAfter:       c.RefreshAfter,
		Token:              masked(c.Token),
		UpdatedAt:          c.UpdatedAt,
	}
}

func settings(s domain.Settings) gen.Settings {
	return gen.Settings{
		FullHashEnabled:     s.FullHashEnabled,
		PrioritiseQuickJobs: s.PrioritiseQuickJobs,
		QsvDevice:           s.QSVDevice,
		QueuePaused:         s.QueuePaused,
		ScanCron:            s.ScanCron,
		ScanEnabled:         s.ScanEnabled,
		ScanRateLimitFps:    s.ScanRateLimitFPS,
		TempDir:             s.TempDir,
		UpdatedAt:           s.UpdatedAt,
	}
}

func plan(p *domain.Plan) *gen.Plan {
	if p == nil {
		return nil
	}

	streams := make([]gen.StreamPlan, 0, len(p.Streams))
	for _, s := range p.Streams {
		streams = append(streams, streamPlan(s))
	}

	out := gen.Plan{
		Deinterlace:           p.Deinterlace,
		DolbyVision:           p.DolbyVision,
		DolbyVisionProfile:    intPtr(p.DolbyVisionProfile),
		Hdr:                   p.HDR,
		Kind:                  gen.PlanKind(p.Kind),
		LevelRewrite:          p.LevelRewrite,
		OutputContainer:       gen.ContainerFamily(p.OutputContainer),
		PolicyHash:            p.PolicyHash,
		Reasons:               nonNilStrings(p.Reasons),
		SourceContainer:       p.SourceContainer,
		Streams:               streams,
		TargetVideoBitrateBps: intPtr(p.TargetVideoBitrate),
	}

	return &out
}

func streamPlan(s domain.StreamPlan) gen.StreamPlan {
	return gen.StreamPlan{
		Comment:          s.Comment,
		Decision:         gen.Decision(s.Decision),
		Default:          s.Default,
		Forced:           s.Forced,
		Language:         strPtr(s.Language),
		OutputIndex:      s.OutputIndex,
		Reason:           s.Reason,
		SourceIndex:      s.SourceIndex,
		TargetBitrateBps: intPtr(s.TargetBitrate),
		TargetChannels:   intPtr(s.TargetChannels),
		TargetCodec:      strPtr(s.TargetCodec),
		Title:            strPtr(s.Title),
		Type:             gen.StreamType(s.Type),
		VisualImpaired:   s.VisualImpaired,
	}
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

func transformRecord(t domain.TransformRecord) gen.TransformRecord {
	audio := make([]gen.AudioTransform, 0, len(t.Audio))
	for _, a := range t.Audio {
		audio = append(audio, gen.AudioTransform{
			Action:      gen.Decision(a.Action),
			After:       audioState(a.After),
			Before:      audioState(a.Before),
			Language:    a.Language,
			OutputIndex: a.OutputIndex,
			Reason:      strPtr(a.Reason),
			SourceIndex: a.SourceIndex,
			Title:       a.Title,
		})
	}

	subs := make([]gen.SubtitleTransform, 0, len(t.Subtitles))
	for _, s := range t.Subtitles {
		subs = append(subs, gen.SubtitleTransform{
			Action:      gen.Decision(s.Action),
			After:       subtitleState(s.After),
			Before:      subtitleState(s.Before),
			Language:    s.Language,
			OutputIndex: s.OutputIndex,
			Reason:      strPtr(s.Reason),
			SourceIndex: s.SourceIndex,
		})
	}

	return gen.TransformRecord{
		Attachments:     gen.BeforeAfterInt{After: t.Attachments.After, Before: t.Attachments.Before},
		Audio:           audio,
		Chapters:        gen.BeforeAfterInt{After: t.Chapters.After, Before: t.Chapters.Before},
		Container:       gen.BeforeAfterString{After: t.Container.After, Before: t.Container.Before},
		DurationSeconds: gen.DurationTransform{Actual: t.Duration.Actual, Estimated: t.Duration.Estimated},
		OutputIdentity:  outputIdentity(t.OutputIdentity),
		Size:            gen.SizeTransform{AfterBytes: t.Size.AfterBytes, BeforeBytes: t.Size.BeforeBytes},
		Subtitles:       subs,
		Video: gen.VideoTransform{
			Action: gen.Decision(t.Video.Action),
			After:  videoState(t.Video.After),
			Before: videoState(t.Video.Before),
			Reason: t.Video.Reason,
		},
	}
}

func outputIdentity(o *domain.OutputIdentity) *gen.OutputIdentity {
	if o == nil {
		return nil
	}

	return &gen.OutputIdentity{
		Fingerprint: o.Fingerprint,
		FullHash:    o.FullHash,
		Mtime:       o.MTime,
		PolicyHash:  o.PolicyHash,
		RecordedAt:  o.RecordedAt,
		SizeBytes:   o.SizeBytes,
	}
}

func videoState(v *domain.VideoState) *gen.VideoState {
	if v == nil {
		return nil
	}

	return &gen.VideoState{
		BitrateKbps: v.BitrateKbps,
		Codec:       v.Codec,
		Fps:         v.FPS,
		Hdr:         v.HDR,
		Height:      v.Height,
		Level:       v.Level,
		PixFmt:      v.PixFmt,
		Profile:     v.Profile,
		Scan:        gen.ScanType(v.Scan),
		Width:       v.Width,
	}
}

func audioState(a *domain.AudioState) *gen.AudioState {
	if a == nil {
		return nil
	}

	return &gen.AudioState{
		BitrateKbps: a.BitrateKbps,
		Channels:    a.Channels,
		Codec:       a.Codec,
		Layout:      a.Layout,
		Profile:     strPtr(a.Profile),
	}
}

func subtitleState(s *domain.SubtitleState) *gen.SubtitleState {
	if s == nil {
		return nil
	}

	return &gen.SubtitleState{Codec: s.Codec, Forced: s.Forced}
}

func event(e domain.Event) gen.Event {
	return gen.Event{
		Category:    e.Category,
		CreatedAt:   e.CreatedAt,
		Id:          e.ID,
		JobId:       e.JobID,
		Level:       gen.EventLevel(e.Level),
		MediaFileId: e.MediaFileID,
		Message:     e.Message,
	}
}

func hwCapability(c domain.HWCapability) gen.HWCapability {
	return gen.HWCapability{
		Backend:       c.Backend,
		Codec:         c.Codec,
		Direction:     gen.HWCapabilityDirection(c.Direction),
		Error:         strPtr(c.Error),
		FfmpegVersion: strPtr(c.FfmpegVersion),
		ProbedAt:      c.ProbedAt,
		Profile:       c.Profile,
		Works:         c.Works,
	}
}

func planKindBreakdown(counts map[domain.Kind]int) gen.PlanKindBreakdown {
	return gen.PlanKindBreakdown{
		AudioOnly: counts[domain.KindAudioOnly],
		Full:      counts[domain.KindFull],
		Remux:     counts[domain.KindRemux],
		Skip:      counts[domain.KindSkip],
	}
}

func filename(path string) string { return filepath.Base(path) }
