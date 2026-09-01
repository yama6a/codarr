package api_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// plan.md 21: there is no authentication, deliberately. Path validation is input
// validation, not authorisation, and it stops a malformed request naming a file
// outside the library.

func TestResolvePlexPath_RejectsTraversalAndPathsOutsideTheRoots(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		path string
		code string
	}{
		"parent traversal":     {"/media/movies/../../etc/passwd", "path_traversal"},
		"encoded-looking dots": {"/media/../etc/shadow", "path_traversal"},
		"outside every root":   {"/etc/passwd", "path_outside_roots"},
		"relative":             {"media/movies/a.mkv", "bad_request"},
		"empty":                {"", "bad_request"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t).withRoots("/media/movies")
			h.store.ListPlexPathMappingsFunc = func(context.Context) ([]domain.PathMapping, error) {
				return nil, nil
			}

			rec := h.do(t, "POST", "/api/plex/resolve-path", gen.ResolvePathRequest{Path: tc.path})
			body := decodeInto[gen.Error](t, rec, 400)

			require.Equal(t, tc.code, body.Error)
		})
	}
}

func TestResolvePlexPath_MapsAPathUnderARoot(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")
	h.store.ListPlexPathMappingsFunc = func(context.Context) ([]domain.PathMapping, error) {
		return []domain.PathMapping{{ID: 3, Local: "/media", Remote: "/data"}}, nil
	}

	got := decodeInto[gen.ResolvePathResult](t,
		h.do(t, "POST", "/api/plex/resolve-path", gen.ResolvePathRequest{Path: "/media/movies/a.mkv"}), 200)

	require.True(t, got.Matched)
	require.Equal(t, "/media/movies/a.mkv", got.LocalPath)
	require.Equal(t, "/data/movies/a.mkv", got.RemotePath)
	require.NotNil(t, got.MappingId)
	require.Equal(t, int64(3), *got.MappingId)
}

func TestCreateRoot_RejectsTraversalRelativeAndOverlappingPaths(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		path string
		code string
	}{
		"traversal":      {"/media/../etc", "path_traversal"},
		"relative":       {"media/movies", "bad_request"},
		"already a root": {"/media/movies", "root_exists"},
		"nested inside":  {"/media/movies/4k", "root_overlaps"},
		"contains":       {"/media", "root_overlaps"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t).withRoots("/media/movies")

			rec := h.do(t, "POST", "/api/roots", gen.RootCreate{Path: tc.path})
			require.Contains(t, []int{400, 409}, rec.Code, rec.Body.String())

			var body gen.Error
			require.NoError(t, decodeJSON(rec.Body.Bytes(), &body))
			require.Equal(t, tc.code, body.Error)
		})
	}
}

func TestCreateRoot_AcceptsANonOverlappingPath(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")
	noInstances(h.store)

	h.store.CreateRootFunc = func(_ context.Context, r domain.Root) (domain.Root, error) {
		r.ID = 2

		return r, nil
	}

	got := decodeInto[gen.Root](t, h.do(t, "POST", "/api/roots", gen.RootCreate{Path: "/media/tv/"}), 201)

	require.Equal(t, "/media/tv", got.Path)
	require.True(t, got.Enabled)
}

func TestAnalyzeMediaFile_RefusesAStoredPathOutsideTheRoots(t *testing.T) {
	t.Parallel()

	h := newHarness(t).withRoots("/media/movies")
	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) {
		return domain.MediaFile{ID: 1, Path: "/somewhere/else/a.mkv"}, nil
	}

	body := decodeInto[gen.Error](t, h.do(t, "POST", "/api/media/1/analyze", nil), 400)

	require.Equal(t, "path_outside_roots", body.Error)
	require.Empty(t, h.analyzer.AnalyzeCalls())
}

func TestUpdateSettings_RejectsARelativeTempDir(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		return domain.Settings{TempDir: "/tmp", QSVDevice: "/dev/dri/renderD128"}, nil
	}

	rec := h.do(t, "PUT", "/api/settings", gen.SettingsUpdate{
		QsvDevice: "/dev/dri/renderD128",
		ScanCron:  "0 4 * * *",
		TempDir:   "tmp/staging",
	})

	body := decodeInto[gen.Error](t, rec, 400)
	require.Equal(t, "bad_request", body.Error)
	require.Empty(t, h.store.UpdateSettingsCalls())
}

func TestUpdatePlex_RejectsARelativePathMapping(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{BaseURL: "http://plex:32400"}
	withPlexConfig(h.store, &cfg)

	rec := h.do(t, "PUT", "/api/plex", gen.PlexConfigUpdate{
		BaseUrl:      "http://plex:32400",
		PathMappings: []gen.PathMapping{{Local: "media", Remote: "/data"}},
		Token:        domain.MaskedSecret,
	})

	require.Equal(t, 400, rec.Code, rec.Body.String())
	require.Empty(t, h.store.ReplacePlexPathMappingsCalls())
}
