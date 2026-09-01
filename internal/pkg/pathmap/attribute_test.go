package pathmap_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

func root(id int64, path string, instance int64) domain.Root {
	r := domain.Root{ID: id, Path: path, Enabled: true, Imported: true}
	if instance != 0 {
		r.ArrInstanceID = &instance
	}

	return r
}

func TestAttribute_LongestPrefixWins(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{
		root(1, "/media/yama", 10),
		root(2, "/media/yama/movies", 11),
		root(3, "/media", 12),
	}

	got, ok := pathmap.Attribute(roots, "/media/yama/movies/Dune (2021)/Dune.mkv")
	require.True(t, ok)
	require.Equal(t, pathmap.Attribution{Root: roots[1], ArrInstanceID: ptr(int64(11))}, got)
}

func TestAttribute_OverlappingRootsResolveToTheInnerOne(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(1, "/media", 10), root(2, "/media/kostas/tv", 20)}

	outer, ok := pathmap.Attribute(roots, "/media/yama/movies/film.mkv")
	require.True(t, ok)
	require.Equal(t, ptr(int64(10)), outer.ArrInstanceID)

	inner, ok := pathmap.Attribute(roots, "/media/kostas/tv/Show/S01E01.mkv")
	require.True(t, ok)
	require.Equal(t, ptr(int64(20)), inner.ArrInstanceID)
}

func TestAttribute_PathUnderNoRoot(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(1, "/media/yama/movies", 10)}

	_, ok := pathmap.Attribute(roots, "/media/kostas/movies/film.mkv")
	require.False(t, ok)

	_, ok = pathmap.Attribute(roots, "/media/yama/movies-4k/film.mkv")
	require.False(t, ok)

	_, ok = pathmap.Attribute(nil, "/media/film.mkv")
	require.False(t, ok)

	_, ok = pathmap.Attribute(roots, "relative.mkv")
	require.False(t, ok)
}

func TestAttribute_TrailingSlashOnTheRootPath(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(1, "/media/yama/movies/", 10)}

	got, ok := pathmap.Attribute(roots, "/media/yama/movies/film.mkv")
	require.True(t, ok)
	require.Equal(t, ptr(int64(10)), got.ArrInstanceID)

	exact, ok := pathmap.Attribute(roots, "/media/yama/movies")
	require.True(t, ok)
	require.Equal(t, ptr(int64(10)), exact.ArrInstanceID)
}

func TestAttribute_IsCaseSensitive(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(1, "/media/yama/movies", 10)}

	_, ok := pathmap.Attribute(roots, "/Media/Yama/Movies/film.mkv")
	require.False(t, ok)
}

func TestAttribute_DisabledRootsAreInvisible(t *testing.T) {
	t.Parallel()

	disabled := root(2, "/media/yama/movies", 11)
	disabled.Enabled = false

	roots := []domain.Root{root(1, "/media", 10), disabled}

	got, ok := pathmap.Attribute(roots, "/media/yama/movies/film.mkv")
	require.True(t, ok)
	require.Equal(t, ptr(int64(10)), got.ArrInstanceID)
}

// A manually added root with no instance: process the files, notify nobody.
func TestAttribute_RootWithoutAnInstance(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(1, "/scratch", 0)}

	got, ok := pathmap.Attribute(roots, "/scratch/film.mkv")
	require.True(t, ok)
	require.Equal(t, pathmap.Attribution{Root: roots[0]}, got)
	require.Nil(t, got.ArrInstanceID)
	require.Nil(t, got.Conflict)
}

// plan.md 16.2: two enabled instances on one root path is a configuration
// error. Report it and notify nobody; never pick one.
func TestAttribute_SameRootClaimedByTwoInstances(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(2, "/media", 20), root(1, "/media/", 10)}

	got, ok := pathmap.Attribute(roots, "/media/film.mkv")
	require.True(t, ok)
	require.Nil(t, got.ArrInstanceID)
	require.Equal(t, &pathmap.Conflict{Path: "/media", InstanceIDs: []int64{10, 20}}, got.Conflict)
}

func TestAttribute_OwnedRootWinsOverAnOwnerlessDuplicate(t *testing.T) {
	t.Parallel()

	roots := []domain.Root{root(1, "/media", 0), root(2, "/media", 20)}

	got, ok := pathmap.Attribute(roots, "/media/film.mkv")
	require.True(t, ok)
	require.Equal(t, ptr(int64(20)), got.ArrInstanceID)
	require.Equal(t, int64(2), got.Root.ID)
	require.Nil(t, got.Conflict)
}

func TestConflicts_ReportsEveryContestedPath(t *testing.T) {
	t.Parallel()

	disabled := root(9, "/media/kostas/tv", 40)
	disabled.Enabled = false

	roots := []domain.Root{
		root(1, "/media", 10),
		root(2, "/media/", 20),
		root(3, "/media", 30),
		root(4, "/archive", 10),
		root(5, "/archive", 10),
		root(6, "/scratch", 0),
		root(7, "/scratch", 0),
		root(8, "/media/kostas/tv", 30),
		disabled,
	}

	require.Equal(t, []pathmap.Conflict{{Path: "/media", InstanceIDs: []int64{10, 20, 30}}}, pathmap.Conflicts(roots))
	require.Empty(t, pathmap.Conflicts(nil))
}

// VERIFY.md, the whole reason this package exists: every one of the four live
// instances reports the literal root "/media". Mapping first keeps them apart.
func TestImportRoots_FourInstancesReportingTheSamePath(t *testing.T) {
	t.Parallel()

	instances := []struct {
		id    int64
		local string
	}{
		{10, "/media/yama/movies"},
		{20, "/media/yama/tv"},
		{30, "/media/kostas/movies"},
		{40, "/media/kostas/tv"},
	}

	candidates := make([]domain.Root, 0, len(instances))

	for _, inst := range instances {
		m := pathmap.New(mappings([2]string{inst.local, "/media"}))

		imported := pathmap.ImportRoots(m, inst.id, []string{"/media"})
		require.Equal(t, []pathmap.ImportedRoot{{
			ArrInstanceID: inst.id,
			ReportedPath:  "/media",
			Path:          inst.local,
			Mapped:        true,
		}}, imported)

		candidates = append(candidates, imported[0].Root())
	}

	require.Empty(t, pathmap.Conflicts(candidates))

	got, ok := pathmap.Attribute(candidates, "/media/kostas/tv/Show/S01E01.mkv")
	require.True(t, ok)
	require.Equal(t, ptr(int64(40)), got.ArrInstanceID)
	require.Nil(t, got.Conflict)
}

// The same four instances with no mappings configured: all four import "/media",
// attribution has four identical candidates, and every file collides.
func TestImportRoots_WithoutMappingsAllFourCollide(t *testing.T) {
	t.Parallel()

	candidates := make([]domain.Root, 0, 4)

	for _, id := range []int64{10, 20, 30, 40} {
		imported := pathmap.ImportRoots(pathmap.New(nil), id, []string{"/media"})
		require.Equal(t, []pathmap.ImportedRoot{{
			ArrInstanceID: id,
			ReportedPath:  "/media",
			Path:          "/media",
			Mapped:        false,
		}}, imported)

		candidates = append(candidates, imported[0].Root())
	}

	require.Equal(t,
		[]pathmap.Conflict{{Path: "/media", InstanceIDs: []int64{10, 20, 30, 40}}},
		pathmap.Conflicts(candidates),
	)

	got, ok := pathmap.Attribute(candidates, "/media/kostas/tv/Show/S01E01.mkv")
	require.True(t, ok)
	require.Nil(t, got.ArrInstanceID)
	require.Equal(t, &pathmap.Conflict{Path: "/media", InstanceIDs: []int64{10, 20, 30, 40}}, got.Conflict)
}

func TestImportRoots_NormalisesAndDropsUnusablePaths(t *testing.T) {
	t.Parallel()

	m := pathmap.New(mappings([2]string{"/media/yama/movies", "/media"}))

	got := pathmap.ImportRoots(m, 10, []string{"/media/", "", "relative", "/other"})

	require.Equal(t, []pathmap.ImportedRoot{
		{ArrInstanceID: 10, ReportedPath: "/media", Path: "/media/yama/movies", Mapped: true},
		{ArrInstanceID: 10, ReportedPath: "/other", Path: "/other", Mapped: false},
	}, got)
}

func TestImportedRoot_BecomesAnEnabledImportedRow(t *testing.T) {
	t.Parallel()

	got := pathmap.ImportedRoot{ArrInstanceID: 7, ReportedPath: "/media", Path: "/media/yama/tv"}.Root()

	require.Equal(t, domain.Root{
		Path:          "/media/yama/tv",
		ArrInstanceID: ptr(int64(7)),
		Imported:      true,
		Enabled:       true,
	}, got)
}

func ptr[T any](v T) *T { return &v }
