package ffmpeg_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func minimalRequest() ffmpeg.Request {
	return ffmpeg.Request{
		Source: ffmpeg.Source{Path: "/library/film.mkv", VideoCodec: "h264"},
		Output: "/library/out.mkv",
		Tags:   tags(),
		Plan: domain.Plan{
			Kind:            domain.KindAudioOnly,
			OutputContainer: domain.ContainerMatroska,
			Streams: []domain.StreamPlan{
				videoCopy(),
				{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng"},
			},
		},
	}
}

func fullRequest() ffmpeg.Request {
	req := minimalRequest()
	req.Encoder = domain.EncoderQSV
	req.Device = "/dev/dri/renderD128"
	req.Plan.Kind = domain.KindFull
	req.Plan.TargetVideoBitrate = 3_000_000
	req.Plan.Streams[0] = videoEncode()

	return req
}

func TestBuild_RejectsInvalidPlans(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*ffmpeg.Request)
		wantErr error
	}{
		{
			name:    "skip plan writes nothing",
			mutate:  func(r *ffmpeg.Request) { r.Plan.Kind = domain.KindSkip },
			wantErr: ffmpeg.ErrNothingToDo,
		},
		{
			name: "no kept video stream",
			mutate: func(r *ffmpeg.Request) {
				r.Plan.Streams[0].Decision = domain.DecisionDrop
			},
			wantErr: ffmpeg.ErrNoVideoStream,
		},
		{
			name: "no kept audio stream",
			mutate: func(r *ffmpeg.Request) {
				r.Plan.Streams = r.Plan.Streams[:1]
			},
			wantErr: ffmpeg.ErrNoAudioStream,
		},
		{
			name: "converted subtitle without a target codec",
			mutate: func(r *ffmpeg.Request) {
				r.Plan.Streams = append(r.Plan.Streams, domain.StreamPlan{
					Type: domain.StreamSubtitle, SourceIndex: 0, Decision: domain.DecisionConvert,
				})
			},
			wantErr: ffmpeg.ErrMissingCodec,
		},
		{
			name: "video encode without an encoder",
			mutate: func(r *ffmpeg.Request) {
				r.Plan.Streams[0] = videoEncode()
				r.Plan.TargetVideoBitrate = 3_000_000
			},
			wantErr: ffmpeg.ErrMissingEncoder,
		},
		{
			name: "video encode without a bitrate",
			mutate: func(r *ffmpeg.Request) {
				r.Plan.Streams[0] = videoEncode()
				r.Encoder = domain.EncoderQSV
				r.Device = "/dev/dri/renderD128"
			},
			wantErr: ffmpeg.ErrMissingBitrate,
		},
		{
			name: "hardware encode without a render device",
			mutate: func(r *ffmpeg.Request) {
				r.Plan.Streams[0] = videoEncode()
				r.Encoder = domain.EncoderQSV
				r.Plan.TargetVideoBitrate = 3_000_000
			},
			wantErr: ffmpeg.ErrMissingDevice,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := minimalRequest()
			tc.mutate(&req)

			got, err := ffmpeg.Build(req)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, ffmpeg.Command{}, got)
		})
	}
}

func TestBuild_SoftwareEncodeNeedsNoDevice(t *testing.T) {
	t.Parallel()

	req := fullRequest()
	req.Encoder = domain.EncoderSoftware
	req.Device = ""

	got, err := ffmpeg.Build(req)
	require.NoError(t, err)
	require.NotContains(t, got.Args, "-init_hw_device")
	require.Contains(t, got.Args, "-pix_fmt")
}

func TestBuild_DecodePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*ffmpeg.Request)
		want   domain.DecodePath
	}{
		{
			name:   "copy needs no decoder at all",
			mutate: func(r *ffmpeg.Request) { *r = minimalRequest() },
			want:   domain.DecodeSoftware,
		},
		{
			name:   "hardware decodable codec",
			mutate: func(*ffmpeg.Request) {},
			want:   domain.DecodeHardware,
		},
		{
			name:   "codec outside the hardware set",
			mutate: func(r *ffmpeg.Request) { r.Source.VideoCodec = "av1" },
			want:   domain.DecodeSoftware,
		},
		{
			name:   "forced software decode after a runtime failure",
			mutate: func(r *ffmpeg.Request) { r.ForceSoftwareDecode = true },
			want:   domain.DecodeSoftware,
		},
		{
			name: "software encoder never takes GPU frames",
			mutate: func(r *ffmpeg.Request) {
				r.Encoder = domain.EncoderSoftware
				r.Device = ""
			},
			want: domain.DecodeSoftware,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := fullRequest()
			tc.mutate(&req)

			got, err := ffmpeg.Build(req)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.DecodePath)
		})
	}
}

func TestHardwareDecodable_Gen95Set(t *testing.T) {
	t.Parallel()

	for _, codec := range []string{"h264", "hevc", "mpeg2video", "vc1", "vp9"} {
		require.True(t, ffmpeg.HardwareDecodable(codec), codec)
	}

	for _, codec := range []string{"av1", "mpeg4", "wmv3", "vp8", ""} {
		require.False(t, ffmpeg.HardwareDecodable(codec), codec)
	}
}

func TestBuild_AudioBitrateFormatting(t *testing.T) {
	t.Parallel()

	req := minimalRequest()
	req.Plan.Streams[1] = domain.StreamPlan{
		Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode,
		TargetCodec: "aac", TargetBitrate: 96_500, Language: "eng",
	}

	got, err := ffmpeg.Build(req)
	require.NoError(t, err)
	require.Contains(t, got.Args, "96500")
}

func TestBuild_EncodedAudioWithoutBitrateOmitsRateControl(t *testing.T) {
	t.Parallel()

	req := minimalRequest()
	req.Plan.Streams[1] = domain.StreamPlan{
		Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionEncode,
		TargetCodec: "aac", Language: "eng",
	}

	got, err := ffmpeg.Build(req)
	require.NoError(t, err)
	require.NotContains(t, got.Args, "-b:a:0")
}
