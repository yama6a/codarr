package api

import (
	"slices"
	"strconv"
	"strings"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// The policy is hard-coded (plan.md 3, rule 3) and this endpoint only displays
// it. Every value below is read from the package that owns it, so the page can
// never show a rule the engine is not applying.

func policy() gen.Policy {
	p := decide.Describe()

	return gen.Policy{
		Audio:      policyAudio(p),
		Bitrate:    policyBitrate(),
		Container:  policyContainer(p),
		Exclusions: policyExclusions(),
		PolicyHash: decide.PolicyHash(),
		SpaceSweep: gen.PolicySpaceSweep{
			ManualOnly:            true,
			MinProjectedSavingPct: job.SweepMinSavingPct,
			MinVideoBitrateKbps:   job.SweepMinBitrate / 1000,
			SourceCodec:           job.SweepVideoCodec,
		},
		Subtitles: policySubtitles(p),
		TagKeys:   decide.TagKeys(),
		Video:     policyVideo(p),
	}
}

func policyVideo(p decide.Snapshot) gen.PolicyVideo {
	return gen.PolicyVideo{
		CopyRule: gen.PolicyVideoCopyRule{
			ChromaSubsampling:        p.CopyChroma,
			Codecs:                   slices.Clone(p.VideoCopyCodecs),
			H264MaxLevel:             trimFloat(p.H264MaxLevel),
			H264Profiles:             slices.Clone(p.H264CopyProfiles),
			HevcProfiles:             slices.Clone(p.HevcCopyProfiles),
			ProgressiveOnly:          true,
			UnknownScanIsProgressive: true,
		},
		DropAttachedPictures: true,
		EncodeTargets: []gen.PolicyEncodeTarget{
			{BitDepth: 8, Codec: p.VideoEncodeCodec, Profile: p.VideoEncodeProfileSDR, Source: "SDR", Mp4Tag: ptrOf("hvc1")},
			{BitDepth: 10, Codec: p.VideoEncodeCodec, Profile: p.VideoEncodeProfileHDR, Source: "HDR", Mp4Tag: ptrOf("hvc1")},
		},
		HardwareDecodeCodecs: decide.HardwareDecodeCodecs(),
		LevelRewrite: gen.PolicyLevelRewrite{
			BitstreamFilter: ffmpeg.LevelRewriteBSF,
			MaxFps:          p.LevelRewriteMaxFPS,
			MaxHeight:       p.LevelRewriteMaxHeight,
			MaxRefs:         p.LevelRewriteMaxRefs,
			MaxWidth:        p.LevelRewriteMaxWidth,
			TargetLevel:     trimFloat(p.LevelRewriteTarget),
		},
	}
}

func policyAudio(p decide.Snapshot) gen.PolicyAudio {
	mp4Surround := decide.AudioEncodeTarget(domain.ContainerMP4, 6)

	return gen.PolicyAudio{
		CopyList: []gen.PolicyAudioCopyRule{
			{Codecs: slices.Clone(p.AudioCopyUpTo2Channels), MinChannels: 1, MaxChannels: 2},
			{Codecs: slices.Clone(p.AudioCopy3PlusChannels), MinChannels: 3, MaxChannels: 0},
		},
		EncodeTargets:         audioEncodeTargets(),
		KeepAllLanguages:      true,
		Mp4KbpsPerChannel:     mp4Surround.Bitrate / mp4Surround.Channels / 1000,
		Mp4MultichannelCodec:  mp4Surround.Codec,
		NeverZeroAudioStreams: true,
	}
}

// audioEncodeTargets describes the Matroska ladder, which is the general case.
// The MP4 divergence is carried by mp4_multichannel_codec and
// mp4_kbps_per_channel rather than by a second ladder.
func audioEncodeTargets() []gen.PolicyAudioEncodeTarget {
	mono := decide.AudioEncodeTarget(domain.ContainerMatroska, 1)
	stereo := decide.AudioEncodeTarget(domain.ContainerMatroska, 2)
	surround := decide.AudioEncodeTarget(domain.ContainerMatroska, 6)
	wide := decide.AudioEncodeTarget(domain.ContainerMatroska, 8)

	return []gen.PolicyAudioEncodeTarget{
		{BitrateKbps: mono.Bitrate / 1000, Codec: mono.Codec, MinChannels: 1, MaxChannels: 1},
		{BitrateKbps: stereo.Bitrate / 1000, Codec: stereo.Codec, MinChannels: 2, MaxChannels: 2},
		{BitrateKbps: surround.Bitrate / 1000, Codec: surround.Codec, MinChannels: 3, MaxChannels: 6},
		{
			BitrateKbps:     wide.Bitrate / 1000,
			Codec:           wide.Codec,
			DownmixChannels: ptrOf(wide.Channels),
			MinChannels:     7,
			MaxChannels:     0,
		},
	}
}

func policySubtitles(p decide.Snapshot) gen.PolicySubtitles {
	return gen.PolicySubtitles{
		DropAttachments:          true,
		DropBroadcastCodecs:      slices.Clone(p.SubtitleBroadcastCodecs),
		DropForcedImageSubtitles: true,
		DropImageCodecs:          slices.Clone(p.SubtitleImageCodecs),
		KeepAllLanguages:         true,
		Targets: []gen.PolicySubtitleTarget{
			{Codec: p.SubtitleTargetMKV, Container: gen.ContainerFamilyMatroska},
			{Codec: p.SubtitleTargetMP4, Container: gen.ContainerFamilyMp4},
		},
		TextCodecs: slices.Clone(p.SubtitleTextCodecs),
	}
}

func policyContainer(p decide.Snapshot) gen.PolicyContainer {
	preserve := make([]string, 0, len(p.ContainerByExt))
	for ext := range p.ContainerByExt {
		preserve = append(preserve, ext)
	}

	slices.Sort(preserve)

	// Legacy is "every video extension the scan accepts that is not preserved",
	// which is where the one filename change in the whole system comes from.
	legacy := make([]string, 0)

	for _, ext := range ingest.VideoExtensions() {
		if !slices.Contains(preserve, ext) {
			legacy = append(legacy, ext)
		}
	}

	return gen.PolicyContainer{
		LegacyExtensions:   legacy,
		Mp4Movflags:        ffmpeg.MP4Movflags(),
		PreserveExtensions: preserve,
	}
}

func policyBitrate() gen.PolicyBitrate {
	resolutions := []ffmpeg.Resolution{
		ffmpeg.Res576p, ffmpeg.Res720p, ffmpeg.Res1080p, ffmpeg.Res1440p, ffmpeg.Res2160p,
	}

	table := make([]gen.PolicyBitrateRow, 0, len(resolutions))
	for _, r := range resolutions {
		table = append(table, gen.PolicyBitrateRow{
			Bpp:         ffmpeg.BPP(r),
			CeilingKbps: ffmpeg.Ceiling(r) / 1000,
			FloorKbps:   ffmpeg.Floor(r) / 1000,
			Resolution:  string(r),
		})
	}

	return gen.PolicyBitrate{
		AppliesTo:     "full jobs and the manual space reclaim sweep",
		BufsizeFactor: ffmpeg.BufsizeFactor,
		FpsScaleCap:   ffmpeg.FPSScaleCap,
		HdrUpliftPct:  (ffmpeg.HDRUplift - 1) * 100,
		MaxrateFactor: ffmpeg.MaxrateFactor,
		SampleProbe: gen.PolicySampleProbe{
			Crf:                ffmpeg.SampleCRF,
			Encoder:            ffmpeg.SampleEncoder,
			HardwareCorrection: ffmpeg.HardwareCorrection,
			Preset:             ffmpeg.SamplePreset,
			SegmentSeconds:     int(ffmpeg.SampleSeconds),
			Segments:           ffmpeg.SampleSegmentCount,
			SkipHeadPct:        ffmpeg.SkipHeadPct * 100,
			SkipTailPct:        ffmpeg.SkipTailPct * 100,
			SourceClamp:        ffmpeg.SourceClamp,
		},
		Table: table,
	}
}

func policyExclusions() gen.PolicyExclusions {
	return gen.PolicyExclusions{
		ExtrasDirectories: ingest.ExtrasDirs(),
		// The scan's own rules, stated the way an operator reads them.
		FilenamePatterns:      []string{".*", "*-trailer.*", "sample.*", "*-sample.*"},
		MinSizeBytes:          ingest.MinSizeBytes,
		PartialExtensions:     ingest.PartialSuffixes(),
		StabilityGuardSeconds: int(ingest.StabilityWindow.Seconds()),
	}
}

// trimFloat renders 4.2 as "4.2" and 5 as "5", which is how levels are spelled
// everywhere else in the UI.
func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)

	return strings.TrimSuffix(s, ".0")
}
