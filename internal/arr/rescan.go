package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// The two rescan commands of plan.md 16.2. Neither triggers an upgrade search:
// upgradeAllowed is false on every quality profile on all four instances (VERIFY.md).
const (
	commandRescanMovie  = "RescanMovie"
	commandRescanSeries = "RescanSeries"
)

// Rescan tells the instance to re-read the item from disk; the path stays stable, so
// this updates the existing file record rather than a delete plus an add.
func (a *API) Rescan(ctx context.Context, item ItemRef) error {
	body, err := rescanCommand(a.identity, item)
	if err != nil {
		return err
	}

	if err := a.tr.do(ctx, call{method: http.MethodPost, path: "/api/v3/command", body: body}); err != nil {
		return fmt.Errorf("arr: rescan on %s failed: %w", a.identity.Name, err)
	}

	return nil
}

func rescanCommand(id Identity, item ItemRef) (map[string]any, error) {
	switch id.Flavour {
	case domain.FlavourRadarr:
		if item.MovieID == 0 {
			return nil, fmt.Errorf("%w: %s needs a movie id to rescan", ErrNoItem, id.Name)
		}

		return map[string]any{"name": commandRescanMovie, "movieId": item.MovieID}, nil
	case domain.FlavourSonarr:
		if item.SeriesID == 0 {
			return nil, fmt.Errorf("%w: %s needs a series id to rescan", ErrNoItem, id.Name)
		}

		return map[string]any{"name": commandRescanSeries, "seriesId": item.SeriesID}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownFlavour, id.Flavour)
	}
}

// Unmonitor is the optional, default-off belt-and-braces of plan.md 16.2, guarding
// against someone re-enabling upgrades later.
func (a *API) Unmonitor(ctx context.Context, item ItemRef) error {
	switch a.identity.Flavour {
	case domain.FlavourRadarr:
		return a.unmonitorMovie(ctx, item.MovieID)
	case domain.FlavourSonarr:
		return a.unmonitorEpisodes(ctx, item.EpisodeIDs)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownFlavour, a.identity.Flavour)
	}
}

// Radarr's PUT replaces the whole resource, so the movie round-trips as a raw object;
// a typed model would blank every field Radarr has added since.
func (a *API) unmonitorMovie(ctx context.Context, movieID int64) error {
	if movieID == 0 {
		return fmt.Errorf("%w: %s needs a movie id to unmonitor", ErrNoItem, a.identity.Name)
	}

	var movie map[string]json.RawMessage

	path := fmt.Sprintf("/api/v3/movie/%d", movieID)
	if err := a.tr.do(ctx, call{method: http.MethodGet, path: path, out: &movie}); err != nil {
		return fmt.Errorf("arr: reading movie %d from %s failed: %w", movieID, a.identity.Name, err)
	}

	if len(movie) == 0 {
		return fmt.Errorf("%w: %s returned no movie %d", ErrNoItem, a.identity.Name, movieID)
	}

	movie["monitored"] = json.RawMessage("false")

	if err := a.tr.do(ctx, call{method: http.MethodPut, path: "/api/v3/movie", body: movie}); err != nil {
		return fmt.Errorf("arr: unmonitoring movie %d on %s failed: %w", movieID, a.identity.Name, err)
	}

	return nil
}

func (a *API) unmonitorEpisodes(ctx context.Context, episodeIDs []int64) error {
	if len(episodeIDs) == 0 {
		return fmt.Errorf("%w: %s needs episode ids to unmonitor", ErrNoItem, a.identity.Name)
	}

	body := map[string]any{"episodeIds": episodeIDs, "monitored": false}

	if err := a.tr.do(ctx, call{method: http.MethodPut, path: "/api/v3/episode/monitor", body: body}); err != nil {
		return fmt.Errorf("arr: unmonitoring %d episodes on %s failed: %w", len(episodeIDs), a.identity.Name, err)
	}

	return nil
}
