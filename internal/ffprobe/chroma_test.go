package ffprobe_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffprobe"
)

func TestChromaOf_ClassifiesSubsamplingNotPixFmtStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pixFmt string
		chroma ffprobe.Chroma
		depth  int
	}{
		{"yuv420p", ffprobe.Chroma420, 8},
		{"yuvj420p", ffprobe.Chroma420, 8},
		{"yuv420p10le", ffprobe.Chroma420, 10},
		{"yuv420p12le", ffprobe.Chroma420, 12},
		{"yuv420p9be", ffprobe.Chroma420, 9},
		{"p010le", ffprobe.Chroma420, 10},
		{"p016le", ffprobe.Chroma420, 16},
		{"p012be", ffprobe.Chroma420, 12},
		{"nv12", ffprobe.Chroma420, 8},
		{"nv21", ffprobe.Chroma420, 8},
		{"yuva420p", ffprobe.Chroma420, 8},
		{"yuv422p", ffprobe.Chroma422, 8},
		{"yuv422p10le", ffprobe.Chroma422, 10},
		{"yuyv422", ffprobe.Chroma422, 8},
		{"uyvy422", ffprobe.Chroma422, 8},
		{"nv16", ffprobe.Chroma422, 8},
		{"nv20le", ffprobe.Chroma422, 10},
		{"p210le", ffprobe.Chroma422, 10},
		{"p216be", ffprobe.Chroma422, 16},
		{"yuv444p", ffprobe.Chroma444, 8},
		{"yuv444p10le", ffprobe.Chroma444, 10},
		{"gbrp", ffprobe.Chroma444, 8},
		{"gbrp10le", ffprobe.Chroma444, 10},
		{"rgb24", ffprobe.Chroma444, 8},
		{"bgra", ffprobe.Chroma444, 8},
		{"pal8", ffprobe.Chroma444, 8},
		{"p410le", ffprobe.Chroma444, 10},
		{"ayuv64le", ffprobe.Chroma444, 16},
		{"yuv411p", ffprobe.ChromaSub, 8},
		{"uyyvyy411", ffprobe.ChromaSub, 8},
		{"yuv410p", ffprobe.ChromaSub, 8},
		{"yuv440p", ffprobe.ChromaSub, 8},
		{"gray", ffprobe.ChromaMono, 8},
		{"gray10le", ffprobe.ChromaMono, 10},
		{"ya8", ffprobe.ChromaMono, 8},
		{"monob", ffprobe.ChromaMono, 8},
		{"", ffprobe.ChromaUnknown, 0},
		{"qsv", ffprobe.ChromaUnknown, 8},
		{"videotoolbox_vld", ffprobe.ChromaUnknown, 8},
		{"YUV420P", ffprobe.Chroma420, 8},
	}

	for _, tc := range tests {
		t.Run(tc.pixFmt, func(t *testing.T) {
			t.Parallel()

			s := ffprobe.Stream{PixFmt: tc.pixFmt}
			require.Equal(t, tc.chroma, s.Chroma())
			require.Equal(t, tc.depth, s.BitDepth())
		})
	}
}
