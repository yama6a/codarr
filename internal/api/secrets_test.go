package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/api/mock"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// plan.md 18.4: API keys and the Plex token are stored in plaintext, but a GET
// never returns one and a PUT only overwrites when the value is not the mask.
// Both directions are tested, for both secrets.

const storedKey = "real-radarr-api-key"

func arrInstanceFixture() domain.ArrInstance {
	return domain.ArrInstance{
		ID:          1,
		Name:        "radarr-4k",
		Flavour:     domain.FlavourRadarr,
		BaseURL:     "http://radarr:7878",
		APIKey:      storedKey,
		WebhookID:   "abc123",
		RescanAfter: true,
		Enabled:     true,
	}
}

func withArrInstance(s *mock.StoreMock, instance *domain.ArrInstance) {
	s.GetArrInstanceFunc = func(context.Context, int64) (domain.ArrInstance, error) { return *instance, nil }
	s.ListArrInstancesFunc = func(context.Context) ([]domain.ArrInstance, error) {
		return []domain.ArrInstance{*instance}, nil
	}
	s.ListArrPathMappingsFunc = func(context.Context, int64) ([]domain.PathMapping, error) { return nil, nil }
	s.ReplaceArrPathMappingsFunc = func(context.Context, int64, []domain.PathMapping) error { return nil }
	s.UpdateArrInstanceFunc = func(_ context.Context, a domain.ArrInstance) error {
		*instance = a

		return nil
	}
}

func TestGetArrInstance_NeverReturnsTheAPIKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	got := decodeInto[gen.ArrInstance](t, h.do(t, "GET", "/api/arr/1", nil), 200)

	require.Equal(t, domain.MaskedSecret, got.ApiKey)
	require.NotContains(t, h.do(t, "GET", "/api/arr/1", nil).Body.String(), storedKey)
}

func TestListArrInstances_NeverReturnsTheAPIKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	rec := h.do(t, "GET", "/api/arr", nil)
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), storedKey)
}

func TestUpdateArrInstance_MaskKeepsTheStoredKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	rec := h.do(t, "PUT", "/api/arr/1", gen.ArrInstanceUpdate{
		ApiKey:       domain.MaskedSecret,
		BaseUrl:      "http://radarr:7878",
		Enabled:      true,
		Flavour:      gen.FlavourRadarr,
		Name:         "radarr-4k",
		PathMappings: []gen.PathMapping{},
		RescanAfter:  true,
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, storedKey, instance.APIKey)
	require.NotContains(t, rec.Body.String(), storedKey)
}

func TestUpdateArrInstance_NewValueReplacesTheStoredKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	rec := h.do(t, "PUT", "/api/arr/1", gen.ArrInstanceUpdate{
		ApiKey:       "rotated-key",
		BaseUrl:      "http://radarr:7878",
		Enabled:      true,
		Flavour:      gen.FlavourRadarr,
		Name:         "radarr-4k",
		PathMappings: []gen.PathMapping{},
		RescanAfter:  true,
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "rotated-key", instance.APIKey)
	require.NotContains(t, rec.Body.String(), "rotated-key")
}

// An empty submission is not a request to clear the key: the field the UI renders
// is a placeholder rather than the value.
func TestUpdateArrInstance_EmptyValueKeepsTheStoredKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	rec := h.do(t, "PUT", "/api/arr/1", gen.ArrInstanceUpdate{
		ApiKey:       "",
		BaseUrl:      "http://radarr:7878",
		Enabled:      true,
		Flavour:      gen.FlavourRadarr,
		Name:         "radarr-4k",
		PathMappings: []gen.PathMapping{},
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, storedKey, instance.APIKey)
}

func TestCreateArrInstance_RequiresARealKeyAndGeneratesTheWebhookID(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	var created domain.ArrInstance

	h.store.CreateArrInstanceFunc = func(_ context.Context, a domain.ArrInstance) (domain.ArrInstance, error) {
		a.ID = 5
		created = a

		return a, nil
	}
	h.store.GetArrInstanceFunc = func(context.Context, int64) (domain.ArrInstance, error) { return created, nil }
	h.store.ListArrPathMappingsFunc = func(context.Context, int64) ([]domain.PathMapping, error) { return nil, nil }
	h.store.ReplaceArrPathMappingsFunc = func(context.Context, int64, []domain.PathMapping) error { return nil }

	masked := h.do(t, "POST", "/api/arr", gen.ArrInstanceCreate{
		ApiKey:  domain.MaskedSecret,
		BaseUrl: "http://sonarr:8989",
		Flavour: gen.FlavourSonarr,
		Name:    "sonarr",
	})
	require.Equal(t, 400, masked.Code, masked.Body.String())

	rec := h.do(t, "POST", "/api/arr", gen.ArrInstanceCreate{
		ApiKey:  "brand-new-key",
		BaseUrl: "http://sonarr:8989",
		Flavour: gen.FlavourSonarr,
		Name:    "sonarr",
	})

	got := decodeInto[gen.ArrInstance](t, rec, 201)
	require.Equal(t, domain.MaskedSecret, got.ApiKey)
	require.Equal(t, "brand-new-key", created.APIKey)
	require.Len(t, created.WebhookID, 32)
	require.NotContains(t, rec.Body.String(), "brand-new-key")
}

const storedToken = "real-plex-token" //nolint:gosec // G101: a test fixture, not a credential

func withPlexConfig(s *mock.StoreMock, cfg *domain.PlexConfig) {
	s.GetPlexConfigFunc = func(context.Context) (domain.PlexConfig, error) { return *cfg, nil }
	s.ListPlexPathMappingsFunc = func(context.Context) ([]domain.PathMapping, error) { return nil, nil }
	s.ReplacePlexPathMappingsFunc = func(context.Context, []domain.PathMapping) error { return nil }
	s.UpdatePlexConfigFunc = func(_ context.Context, c domain.PlexConfig) error {
		*cfg = c

		return nil
	}
}

func TestGetPlex_NeverReturnsTheToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{BaseURL: "http://plex:32400", Token: storedToken, GuardActiveStreams: true}
	withPlexConfig(h.store, &cfg)

	rec := h.do(t, "GET", "/api/plex", nil)
	got := decodeInto[gen.PlexConfig](t, rec, 200)

	require.Equal(t, domain.MaskedSecret, got.Token)
	require.NotContains(t, rec.Body.String(), storedToken)
}

func TestUpdatePlex_MaskKeepsTheStoredToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{BaseURL: "http://plex:32400", Token: storedToken, GuardActiveStreams: true}
	withPlexConfig(h.store, &cfg)

	rec := h.do(t, "PUT", "/api/plex", gen.PlexConfigUpdate{
		AnalyzeAfter:       true,
		BaseUrl:            "http://plex:32400",
		GuardActiveStreams: true,
		PathMappings:       []gen.PathMapping{},
		RefreshAfter:       true,
		Token:              domain.MaskedSecret,
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, storedToken, cfg.Token)
	require.NotContains(t, rec.Body.String(), storedToken)
}

func TestUpdatePlex_NewValueReplacesTheStoredToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{BaseURL: "http://plex:32400", Token: storedToken}
	withPlexConfig(h.store, &cfg)

	rec := h.do(t, "PUT", "/api/plex", gen.PlexConfigUpdate{
		BaseUrl:      "http://plex:32400",
		PathMappings: []gen.PathMapping{},
		Token:        "rotated-token",
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "rotated-token", cfg.Token)
	require.NotContains(t, rec.Body.String(), "rotated-token")
}

// The PIN poll is the one path where a secret is legitimately in flight. It
// stores the token and reports that it did, and never returns it.
func TestPollPlexAuth_StoresTheTokenAndNeverReturnsIt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{ClientIdentifier: "client-id"}
	withPlexConfig(h.store, &cfg)

	h.plexAuth.CheckPinFunc = func(context.Context, string, int64) (plexPin, error) {
		return plexPin{ID: 99, Code: "ABCD", AuthToken: "claimed-token"}, nil
	}

	rec := h.do(t, "GET", "/api/plex/auth/poll/99", nil)
	got := decodeInto[gen.PlexAuthPoll](t, rec, 200)

	require.True(t, got.Authorized)
	require.True(t, got.TokenStored)
	require.Equal(t, "claimed-token", cfg.Token)
	require.NotContains(t, rec.Body.String(), "claimed-token")
}

func TestPollPlexAuth_UnclaimedPinStoresNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{ClientIdentifier: "client-id"}
	withPlexConfig(h.store, &cfg)

	h.plexAuth.CheckPinFunc = func(context.Context, string, int64) (plexPin, error) {
		return plexPin{ID: 99, Code: "ABCD"}, nil
	}

	got := decodeInto[gen.PlexAuthPoll](t, h.do(t, "GET", "/api/plex/auth/poll/99", nil), 200)

	require.False(t, got.Authorized)
	require.False(t, got.TokenStored)
	require.Empty(t, cfg.Token)
}

func TestStartPlexAuth_GeneratesAndPersistsTheClientIdentifier(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{}
	withPlexConfig(h.store, &cfg)

	expires := testNow.Add(time.Minute)
	h.plexAuth.CreatePinFunc = func(_ context.Context, id string) (plexPin, error) {
		require.NotEmpty(t, id)

		return plexPin{ID: 7, Code: "WXYZ", ClientIdentifier: id, ExpiresAt: &expires}, nil
	}
	h.plexAuth.AuthURLFunc = func(_, code string) string { return "https://app.plex.tv/auth#?code=" + code }

	got := decodeInto[gen.PlexAuthStart](t, h.do(t, "POST", "/api/plex/auth/start", nil), 200)

	require.Equal(t, int64(7), got.PinId)
	require.Equal(t, "WXYZ", got.Code)
	require.NotEmpty(t, got.ClientIdentifier)
	require.Equal(t, got.ClientIdentifier, cfg.ClientIdentifier)
}
