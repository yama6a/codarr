package ffmpeg_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

var update = flag.Bool("update", false, "rewrite the argv golden files")

func idx(i int) *int { return &i }

func tags() ffmpeg.Tags {
	return ffmpeg.Tags{Version: "0.1.0", Policy: "a1b2c3d4"}
}

func requireGolden(t *testing.T, name string, args []string) {
	t.Helper()

	path := filepath.Join("testdata", name+".argv")

	if *update {
		require.NoError(t, os.WriteFile(path, []byte(strings.Join(args, "\n")+"\n"), 0o600))
	}

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	want := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	require.Equal(t, want, args)
}

func videoCopy() domain.StreamPlan {
	return domain.StreamPlan{
		Type: domain.StreamVideo, SourceIndex: 0, OutputIndex: idx(0),
		Decision: domain.DecisionCopy, Default: true,
	}
}

func videoEncode() domain.StreamPlan {
	return domain.StreamPlan{
		Type: domain.StreamVideo, SourceIndex: 0, OutputIndex: idx(0),
		Decision: domain.DecisionEncode, TargetCodec: "hevc", Default: true,
	}
}

func TestBuild_Golden(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  ffmpeg.Request
	}{
		{
			// plan.md 14.1, the dominant audio_only case.
			name: "audio_only_mkv",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/film.mkv", VideoCodec: "h264"},
				Output: "/library/.codarr-staging-1.mkv",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:            domain.KindAudioOnly,
					SourceContainer: "matroska,webm",
					OutputContainer: domain.ContainerMatroska,
					Streams: []domain.StreamPlan{
						videoCopy(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Title: "Surround 5.1", Default: true},
						{Type: domain.StreamAudio, SourceIndex: 1, Decision: domain.DecisionCopy, Language: "ger"},
						{Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionDrop, Language: "eng"},
						{Type: domain.StreamSubtitle, SourceIndex: 1, Decision: domain.DecisionCopy, Language: "eng", Default: true},
						{Type: domain.StreamSubtitle, SourceIndex: 2, Decision: domain.DecisionConvert, TargetCodec: "srt", Language: "swe", Forced: true},
					},
				},
			},
		},
		{
			// plan.md 14.2: every indexed option addresses the OUTPUT position.
			// Source video 0, subtitles 0 and 2 are dropped, so source video 1
			// becomes v:0 and source subtitles 1, 3, 4 become s:0, s:1, s:2.
			name: "output_index_trap",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/trap.mkv", VideoCodec: "h264"},
				Output: "/library/.codarr-staging-2.mkv",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:            domain.KindAudioOnly,
					SourceContainer: "matroska,webm",
					OutputContainer: domain.ContainerMatroska,
					Streams: []domain.StreamPlan{
						{Type: domain.StreamVideo, SourceIndex: 0, Decision: domain.DecisionDrop, Reason: "attached picture"},
						{Type: domain.StreamVideo, SourceIndex: 1, Decision: domain.DecisionCopy, Default: true},
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
						{Type: domain.StreamAudio, SourceIndex: 1, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "ger"},
						{Type: domain.StreamAudio, SourceIndex: 2, Decision: domain.DecisionCopy, Language: "eng", Comment: true},
						{Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionDrop, Language: "eng"},
						{Type: domain.StreamSubtitle, SourceIndex: 1, Decision: domain.DecisionConvert, TargetCodec: "srt", Language: "eng", Default: true},
						{Type: domain.StreamSubtitle, SourceIndex: 2, Decision: domain.DecisionDrop, Language: "swe", Forced: true},
						{Type: domain.StreamSubtitle, SourceIndex: 3, Decision: domain.DecisionCopy, Language: "swe"},
						{Type: domain.StreamSubtitle, SourceIndex: 4, Decision: domain.DecisionConvert, TargetCodec: "srt", Language: "fin", Forced: true, VisualImpaired: true},
					},
				},
			},
		},
		{
			name: "audio_only_mp4",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/film.mp4", VideoCodec: "hevc"},
				Output: "/library/.codarr-staging-3.mp4",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:            domain.KindAudioOnly,
					SourceContainer: "mov,mp4,m4a,3gp,3g2,mj2",
					OutputContainer: domain.ContainerMP4,
					Streams: []domain.StreamPlan{
						videoCopy(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "aac", TargetBitrate: 384_000, TargetChannels: 6, Language: "eng", Default: true},
						{Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionConvert, TargetCodec: "mov_text", Language: "eng"},
					},
				},
			},
		},
		{
			// plan.md 6.2: level-only failure that fits 4.2 is a flag rewrite.
			name: "level_rewrite",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/level51.mkv", VideoCodec: "h264"},
				Output: "/library/.codarr-staging-4.mkv",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:            domain.KindAudioOnly,
					SourceContainer: "matroska,webm",
					OutputContainer: domain.ContainerMatroska,
					LevelRewrite:    true,
					Streams: []domain.StreamPlan{
						videoCopy(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			name: "level_rewrite_mp4",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/level51.mp4", VideoCodec: "h264"},
				Output: "/library/.codarr-staging-5.mp4",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:            domain.KindAudioOnly,
					SourceContainer: "mov,mp4,m4a,3gp,3g2,mj2",
					OutputContainer: domain.ContainerMP4,
					LevelRewrite:    true,
					Streams: []domain.StreamPlan{
						videoCopy(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "aac", TargetBitrate: 160_000, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 6.1: legacy containers become MKV and need +genpts.
			name: "remux_legacy_avi",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/old.avi", VideoCodec: "h264", LegacyContainer: true},
				Output: "/library/.codarr-staging-6.mkv",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:            domain.KindRemux,
					SourceContainer: "avi",
					OutputContainer: domain.ContainerMatroska,
					Streams: []domain.StreamPlan{
						videoCopy(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 14.1, full on the hardware-decode path.
			name: "full_hw_decode",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/high10.mkv", VideoCodec: "h264"},
				Output:  "/library/.codarr-staging-7.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 3_500_000,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Default: true},
						{Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng"},
					},
				},
			},
		},
		{
			// plan.md 9: vpp_qsv on the hardware path, never bwdif.
			name: "full_hw_decode_interlaced",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/dvd.mkv", VideoCodec: "mpeg2video"},
				Output:  "/library/.codarr-staging-8.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 2_500_000,
					Deinterlace:        true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 14.1: AV1 has no QSV decoder on Gen 9.5.
			name: "full_sw_decode_av1",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/av1.mkv", VideoCodec: "av1"},
				Output:  "/library/.codarr-staging-9.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 4_000_000,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 9: bwdif runs before format/hwupload, on CPU frames.
			name: "full_sw_decode_legacy_interlaced",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/xvid.avi", VideoCodec: "mpeg4", LegacyContainer: true},
				Output:  "/library/.codarr-staging-10.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "avi",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 1_500_000,
					Deinterlace:        true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 9: never tonemap; Main 10 plus the colour flags.
			name: "full_hdr_hw_decode",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/hdr.mkv", VideoCodec: "hevc"},
				Output:  "/library/.codarr-staging-11.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 12_000_000,
					HDR:                true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 14.1 mandates -tag:v hvc1 for HEVC in MP4, but on a Dolby
			// Vision source that silently destroys the dvcC record. Verified on
			// jellyfin-ffmpeg 7.1.4: the mov muxer needs dvh1 plus -strict
			// unofficial, and warns at no log level when it declines.
			name: "audio_only_mp4_dolby_vision",
			req: ffmpeg.Request{
				Source: ffmpeg.Source{Path: "/library/dv.mp4", VideoCodec: "hevc"},
				Output: "/library/.codarr-staging-19.mp4",
				Tags:   tags(),
				Plan: domain.Plan{
					Kind:               domain.KindAudioOnly,
					SourceContainer:    "mp4",
					OutputContainer:    domain.ContainerMP4,
					HDR:                true,
					DolbyVision:        true,
					DolbyVisionProfile: 5,
					Streams: []domain.StreamPlan{
						{Type: domain.StreamVideo, SourceIndex: 0, Decision: domain.DecisionCopy, Default: true},
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "aac", TargetBitrate: 384_000, TargetChannels: 6, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 9 calls a stream HDR on smpte2084 OR arib-std-b67, then
			// prescribes -color_trc smpte2084 unconditionally. An HLG source
			// stamped PQ renders wrong, so the transfer is carried through.
			name: "full_hdr_hlg_hw_decode",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/hlg.mkv", VideoCodec: "hevc"},
				Output:  "/library/.codarr-staging-18.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 12_000_000,
					HDR:                true,
					HDRTransfer:        "arib-std-b67",
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "ac3", TargetBitrate: 640_000, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 9: p010le lives in the filter chain, never in -pix_fmt.
			name: "full_hdr_sw_decode",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/hdr_av1.mkv", VideoCodec: "av1"},
				Output:  "/library/.codarr-staging-12.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 12_000_000,
					HDR:                true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 14.1: -pix_fmt only on the pure software encode.
			name: "full_software_encode",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/wmv.mkv", VideoCodec: "wmv3"},
				Output:  "/library/.codarr-staging-13.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderSoftware,
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 2_500_000,
					Deinterlace:        true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			name: "full_software_encode_hdr",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/hdr_sw.mkv", VideoCodec: "av1"},
				Output:  "/library/.codarr-staging-14.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderSoftware,
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 12_000_000,
					HDR:                true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 6.2: hvc1, movflags and mov_text all at once.
			name: "full_mp4",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/film.mp4", VideoCodec: "h264"},
				Output:  "/library/.codarr-staging-15.mp4",
				Tags:    tags(),
				Encoder: domain.EncoderQSV,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "mov,mp4,m4a,3gp,3g2,mj2",
					OutputContainer:    domain.ContainerMP4,
					TargetVideoBitrate: 3_000_000,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode, TargetCodec: "aac", TargetBitrate: 160_000, Language: "eng", Default: true},
						{Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionConvert, TargetCodec: "mov_text", Language: "eng"},
					},
				},
			},
		},
		{
			// plan.md 10.2: VAAPI is the fallback backend, with its own filter.
			name: "full_vaapi_interlaced",
			req: ffmpeg.Request{
				Source:  ffmpeg.Source{Path: "/library/vc1.mkv", VideoCodec: "vc1"},
				Output:  "/library/.codarr-staging-16.mkv",
				Tags:    tags(),
				Encoder: domain.EncoderVAAPI,
				Device:  "/dev/dri/renderD128",
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 3_500_000,
					Deinterlace:        true,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
		{
			// plan.md 10.1: the retry after a hardware decode failed at runtime.
			name: "full_forced_software_decode",
			req: ffmpeg.Request{
				Source:              ffmpeg.Source{Path: "/library/quirk.mkv", VideoCodec: "h264"},
				Output:              "/library/.codarr-staging-17.mkv",
				Tags:                tags(),
				Encoder:             domain.EncoderQSV,
				Device:              "/dev/dri/renderD128",
				ForceSoftwareDecode: true,
				Plan: domain.Plan{
					Kind:               domain.KindFull,
					SourceContainer:    "matroska,webm",
					OutputContainer:    domain.ContainerMatroska,
					TargetVideoBitrate: 3_500_000,
					Streams: []domain.StreamPlan{
						videoEncode(),
						{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ffmpeg.Build(tc.req)
			require.NoError(t, err)
			requireGolden(t, tc.name, got.Args)
		})
	}
}
