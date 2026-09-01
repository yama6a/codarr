package arr_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

func ptr[T any](v T) *T { return &v }

// The fixture is a real capture: every one of the four live instances answers
// with the literal "/media", because each mounts only its own slice via a
// subPath (VERIFY.md).
func TestRootFolders_MapsTheReportedPathIntoCodarrsView(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {fixture: "radarr_rootfolder.json"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	got, err := c.RootFolders(context.Background())
	require.NoError(t, err)

	require.Equal(t, []arr.RootFolder{{
		ID:         1,
		Accessible: true,
		FreeSpace:  ptr(int64(23833623920640)),
		Imported: pathmap.ImportedRoot{
			ArrInstanceID: 1,
			ReportedPath:  "/media",
			Path:          "/media/yama/movies",
			Mapped:        true,
		},
	}}, got)
}

// Two instances with their own mappings resolve the same reported path to two
// different directories. Without the mapping they would produce identical roots
// and the longest-prefix attribution of plan.md 16.2 could not tell them apart.
func TestRootFolders_KeepsTwoInstancesApartDespiteTheSameReportedPath(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {fixture: "radarr_rootfolder.json"},
	})

	movies, err := newClient(t, s, radarrYama(), radarrMapper()).RootFolders(context.Background())
	require.NoError(t, err)

	tv, err := newClient(t, s, sonarrYama(), sonarrMapper()).RootFolders(context.Background())
	require.NoError(t, err)

	require.Equal(t, "/media/yama/movies", movies[0].Imported.Path)
	require.Equal(t, "/media/yama/tv", tv[0].Imported.Path)
	require.Empty(t, pathmap.Conflicts([]domain.Root{movies[0].Imported.Root(), tv[0].Imported.Root()}))
}

func TestRootFolders_FlagsAnInstanceWithNoMapping(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {fixture: "radarr_rootfolder.json"},
	})
	c := newClient(t, s, radarrYama(), nil)

	got, err := c.RootFolders(context.Background())
	require.NoError(t, err)

	require.Equal(t, pathmap.ImportedRoot{
		ArrInstanceID: 1,
		ReportedPath:  "/media",
		Path:          "/media",
		Mapped:        false,
	}, got[0].Imported)
}

func TestImportRoots_ReturnsTheRootsToCreate(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {fixture: "sonarr_rootfolder.json"},
	})
	c := newClient(t, s, sonarrYama(), sonarrMapper())

	got, err := arr.ImportRoots(context.Background(), c)
	require.NoError(t, err)

	require.Equal(t, []pathmap.ImportedRoot{{
		ArrInstanceID: 3,
		ReportedPath:  "/media",
		Path:          "/media/yama/tv",
		Mapped:        true,
	}}, got)

	require.Equal(t, domain.Root{
		Path:          "/media/yama/tv",
		ArrInstanceID: ptr(int64(3)),
		Imported:      true,
		Enabled:       true,
	}, got[0].Root())
}

// Importing "/media" verbatim gives four identical roots on this cluster, so
// the import is refused with a message that names the fix.
func TestImportRoots_RefusesWhenNoMappingRewroteThePath(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {fixture: "radarr_rootfolder.json"},
	})
	c := newClient(t, s, radarrYama(), nil)

	_, err := arr.ImportRoots(context.Background(), c)
	require.ErrorIs(t, err, arr.ErrNoPathMapping)
	require.Contains(t, err.Error(), "radarr-yama reports /media")
	require.Contains(t, err.Error(), "Add a path mapping for this instance")
}

func TestImportRoots_PropagatesAFailedListing(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {status: 401},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	_, err := arr.ImportRoots(context.Background(), c)
	require.ErrorIs(t, err, arr.ErrUnauthorized)
}
