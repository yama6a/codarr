package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestContainer_OutputExtNeverRenamesAnMKVOrMP4(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		container domain.Container
		source    string
		want      string
	}{
		{"mkv stays mkv", domain.ContainerMatroska, "/media/film.mkv", ".mkv"},
		{"mkv is case insensitive", domain.ContainerMatroska, "/media/film.MKV", ".MKV"},
		{"mp4 stays mp4", domain.ContainerMP4, "/media/film.mp4", ".mp4"},
		// plan.md 6.1: the filename must not change, so m4v must not become mp4.
		{"m4v stays m4v", domain.ContainerMP4, "/media/film.m4v", ".m4v"},
		{"m4v is case insensitive", domain.ContainerMP4, "/media/film.M4V", ".M4V"},
		{"avi becomes mkv", domain.ContainerMatroska, "/media/film.avi", ".mkv"},
		{"vob becomes mkv", domain.ContainerMatroska, "/media/film.vob", ".mkv"},
		{"no extension becomes mkv", domain.ContainerMatroska, "/media/film", ".mkv"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.container.OutputExt(tc.source))
		})
	}
}
