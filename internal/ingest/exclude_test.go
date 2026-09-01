package ingest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ingest"
)

const big = 2 << 30

func TestExcludeFile_AppliesTheHardCodedRules(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		path string
		size int64
		want ingest.Exclusion
	}{
		{"a normal film", "/media/yama/movies/Heat (1995)/Heat (1995).mkv", big, ingest.NotExcluded},
		{"a normal episode", "/media/yama/tv/Show/S01/Show - S01E01.mp4", big, ingest.NotExcluded},
		{"a legacy container", "/media/yama/movies/Old/Old.avi", big, ingest.NotExcluded},

		{"trailer suffix", "/media/m/Heat-trailer.mkv", big, ingest.ExcludedTrailer},
		{"trailer suffix uppercase", "/media/m/Heat-Trailer.MKV", big, ingest.ExcludedTrailer},
		{"sample suffix", "/media/m/Heat-sample.mkv", big, ingest.ExcludedSample},
		{"bare sample", "/media/m/sample.mkv", big, ingest.ExcludedSample},
		{"bare sample uppercase", "/media/m/Sample.mkv", big, ingest.ExcludedSample},
		{"trailer is not a substring rule", "/media/m/The Trailer Park Boys.mkv", big, ingest.NotExcluded},

		{"qbittorrent part", "/media/m/Heat.mkv.!qB", big, ingest.ExcludedPartial},
		{"generic part", "/media/m/Heat.mkv.part", big, ingest.ExcludedPartial},
		{"partial", "/media/m/Heat.mkv.partial", big, ingest.ExcludedPartial},
		{"tmp", "/media/m/Heat.mkv.tmp", big, ingest.ExcludedPartial},

		{"dotfile", "/media/m/.DS_Store", big, ingest.ExcludedHidden},
		{"codarr staging file", "/media/m/.codarr-staging-42-Heat.mkv", big, ingest.ExcludedHidden},

		{"subtitle sidecar", "/media/m/Heat.srt", 1000, ingest.ExcludedExtension},
		{"artwork", "/media/m/poster.jpg", 1000, ingest.ExcludedExtension},
		{"no extension", "/media/m/Heat", big, ingest.ExcludedExtension},

		{"under the floor", "/media/m/Heat.mkv", ingest.MinSizeBytes - 1, ingest.ExcludedTooSmall},
		{"exactly the floor", "/media/m/Heat.mkv", ingest.MinSizeBytes, ingest.NotExcluded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ingest.ExcludeFile(tc.path, tc.size))
		})
	}
}

// plan.md 15.1 names staging files .codarr-staging-* and 13.3 excludes dotfiles; this
// pins the two meeting, so a scan can never pick up a transcode in flight.
func TestExcludeFile_NeverSeesCodarrsOwnStagingFiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"/media/m/.codarr-staging-1-Heat.mkv",
		"/media/m/.codarr-staging-999-Some Show - S01E01.mp4",
		"/media/m/.codarr-vp9-probe.webm",
	} {
		require.Equal(t, ingest.ExcludedHidden, ingest.ExcludeFile(name, big), name)
	}
}

func TestExcludeDir_PrunesThePlexExtrasDirs(t *testing.T) {
	t.Parallel()

	for _, d := range ingest.ExtrasDirs() {
		require.True(t, ingest.ExcludeDir(d), d)
	}

	require.True(t, ingest.ExcludeDir("featurettes"), "capitalisation is not load-bearing")
	require.False(t, ingest.ExcludeDir("Season 01"))
	require.False(t, ingest.ExcludeDir("Other Stuff"))
	require.False(t, ingest.ExcludeDir(""))
}

func TestExtrasDirs_IsThePlanList(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"Behind The Scenes", "Deleted Scenes", "Featurettes", "Interviews",
		"Scenes", "Shorts", "Trailers", "Other",
	}, ingest.ExtrasDirs())
}

func TestPartialSuffixes_IsThePlanList(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{".part", ".!qb", ".partial", ".tmp"}, ingest.PartialSuffixes())
}
