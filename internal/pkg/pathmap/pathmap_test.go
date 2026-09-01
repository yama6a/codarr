package pathmap_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

func mappings(pairs ...[2]string) []domain.PathMapping {
	out := make([]domain.PathMapping, 0, len(pairs))

	for i, p := range pairs {
		out = append(out, domain.PathMapping{ID: int64(i + 1), Local: p[0], Remote: p[1], Sort: i})
	}

	return out
}

func TestMapper_RewritesBothDirections(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/media/yama/movies", "/media"}))

	local, ok := m.ToLocal("/media/Dune (2021)/Dune.mkv")
	require.True(t, ok)
	require.Equal(t, "/media/yama/movies/Dune (2021)/Dune.mkv", local)

	remote, ok := m.ToRemote("/media/yama/movies/Dune (2021)/Dune.mkv")
	require.True(t, ok)
	require.Equal(t, "/media/Dune (2021)/Dune.mkv", remote)
}

func TestMapper_MapsThePrefixItself(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/media/yama/tv", "/media"}))

	local, ok := m.ToLocal("/media")
	require.True(t, ok)
	require.Equal(t, "/media/yama/tv", local)

	remote, ok := m.ToRemote("/media/yama/tv")
	require.True(t, ok)
	require.Equal(t, "/media", remote)
}

// Longest prefix first, or a nested mapping is shadowed by its parent.
func TestMapper_LongestPrefixWins(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings(
		[2]string{"/mnt/pool/media", "/data"},
		[2]string{"/mnt/pool/four-k", "/data/uhd"},
		[2]string{"/mnt/other", "/data/uhd/remux"},
	))

	for _, tc := range []struct{ remote, want string }{
		{"/data/show/ep.mkv", "/mnt/pool/media/show/ep.mkv"},
		{"/data/uhd/film.mkv", "/mnt/pool/four-k/film.mkv"},
		{"/data/uhd/remux/film.mkv", "/mnt/other/film.mkv"},
	} {
		got, ok := m.ToLocal(tc.remote)
		require.True(t, ok, tc.remote)
		require.Equal(t, tc.want, got)
	}
}

func TestMapper_MatchesWholeComponentsOnly(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/local/media", "/media"}))

	got, ok := m.ToLocal("/media-archive/film.mkv")
	require.False(t, ok)
	require.Equal(t, "/media-archive/film.mkv", got)
}

func TestMapper_TrailingSlashesAndDoubleSeparators(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/media/yama/movies/", "/media/"}))

	for _, in := range []string{"/media/film.mkv", "/media//film.mkv", "/media/./film.mkv"} {
		got, ok := m.ToLocal(in)
		require.True(t, ok, in)
		require.Equal(t, "/media/yama/movies/film.mkv", got)
	}

	got, ok := m.ToLocal("/media/")
	require.True(t, ok)
	require.Equal(t, "/media/yama/movies", got)
}

// Linux. /Media is a different directory from /media, and nothing here folds case.
func TestMapper_IsCaseSensitive(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/local/media", "/media"}))

	got, ok := m.ToLocal("/Media/film.mkv")
	require.False(t, ok)
	require.Equal(t, "/Media/film.mkv", got)
}

func TestMapper_RootPrefix(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/mnt/nas", "/"}))

	got, ok := m.ToLocal("/media/film.mkv")
	require.True(t, ok)
	require.Equal(t, "/mnt/nas/media/film.mkv", got)
}

func TestMapper_UnmappedAndInvalidPaths(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/local", "/remote"}))

	got, ok := m.ToLocal("/elsewhere/film.mkv")
	require.False(t, ok)
	require.Equal(t, "/elsewhere/film.mkv", got)

	got, ok = m.ToLocal("relative/film.mkv")
	require.False(t, ok)
	require.Equal(t, "relative/film.mkv", got)

	got, ok = m.ToLocal("")
	require.False(t, ok)
	require.Empty(t, got)
}

// Plex needs no mapping on this cluster (VERIFY.md); the empty mapper must be a
// clean no-op rather than a special case at every call site.
func TestMapper_EmptySetIsANoOp(t *testing.T) {
	t.Parallel()

	m := pathmap.New(nil)

	got, ok := m.ToLocal("/media/yama/movies/film.mkv")
	require.False(t, ok)
	require.Equal(t, "/media/yama/movies/film.mkv", got)
}

func TestMapper_SkipsIncompleteMappings(t *testing.T) {
	t.Parallel()

	m := pathmap.New([]domain.PathMapping{
		{ID: 1, Local: "", Remote: "/media"},
		{ID: 2, Local: "/local", Remote: ""},
		{ID: 3, Local: "relative", Remote: "/media"},
		{ID: 4, Local: "/local/media", Remote: "/media"},
	})

	got, ok := m.ToLocal("/media/film.mkv")
	require.True(t, ok)
	require.Equal(t, "/local/media/film.mkv", got)
}

func TestUnderPrefix_ComponentBoundaries(t *testing.T) {
	t.Parallel()

	require.True(t, pathmap.UnderPrefix("/media", "/media"))
	require.True(t, pathmap.UnderPrefix("/media/a", "/media"))
	require.True(t, pathmap.UnderPrefix("/media/a", "/"))
	require.False(t, pathmap.UnderPrefix("/mediafoo", "/media"))
	require.False(t, pathmap.UnderPrefix("/med", "/media"))
}

func TestNormalise_RejectsRelativePaths(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/media", pathmap.Normalise("/media/"))
	require.Equal(t, "/media/a", pathmap.Normalise("//media//a/"))
	require.Equal(t, "/", pathmap.Normalise("/"))
	require.Empty(t, pathmap.Normalise("media/a"))
	require.Empty(t, pathmap.Normalise(""))
}
