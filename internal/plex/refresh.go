package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// Plex item type codes for filtering a section listing: a show section's /all returns
// series, not files, so a rating key by path needs the episode type explicitly.
const (
	typeMovie   = "1"
	typeEpisode = "4"
)

// RefreshPath runs the partial scan of plan.md 16.1 step 1 over the directory
// containing localPath.
func (c *Client) RefreshPath(ctx context.Context, localPath string) error {
	target, err := c.Resolve(ctx, localPath)
	if err != nil {
		return err
	}

	return c.RefreshDir(ctx, target)
}

// RefreshDir scans one already-resolved directory.
func (c *Client) RefreshDir(ctx context.Context, target Target) error {
	err := c.tr.do(ctx, request{
		method: http.MethodGet,
		path:   "/library/sections/" + target.Section.Key + "/refresh",
		query:  url.Values{"path": {target.RemoteDir}},
	})
	if err != nil {
		return fmt.Errorf("plex: refreshing section %s at %s failed: %w", target.Section.Key, target.RemoteDir, err)
	}

	return nil
}

// Analyze is plan.md 16.1 step 2, updating media info in place because a delete and
// rescan would destroy watch state, ratings, playlists and collections.
//
// VERIFY.md item 2 is open: the verb is unconfirmed against the running PMS.
func (c *Client) Analyze(ctx context.Context, ratingKey string) error {
	if ratingKey == "" {
		return fmt.Errorf("%w: no rating key", ErrNoRatingKey)
	}

	err := c.tr.do(ctx, request{
		method: http.MethodPut,
		path:   "/library/metadata/" + ratingKey + "/analyze",
	})
	if err != nil {
		return fmt.Errorf("plex: analyzing item %s failed: %w", ratingKey, err)
	}

	return nil
}

// RatingKeyFor finds the library item whose file is localPath, confirming against the
// returned parts because Plex accepts and silently ignores an unknown filter.
func (c *Client) RatingKeyFor(ctx context.Context, localPath string) (string, error) {
	target, err := c.Resolve(ctx, localPath)
	if err != nil {
		return "", err
	}

	return c.ratingKeyIn(ctx, target)
}

func (c *Client) ratingKeyIn(ctx context.Context, target Target) (string, error) {
	query := url.Values{"file": {target.RemotePath}}
	if t, ok := itemType(target.Section.Type); ok {
		query.Set("type", t)
	}

	var resp metadataResponse

	err := c.tr.do(ctx, request{
		method: http.MethodGet,
		path:   "/library/sections/" + target.Section.Key + "/all",
		query:  query,
		out:    &resp,
	})
	if err != nil {
		return "", fmt.Errorf("plex: searching section %s for %s failed: %w",
			target.Section.Key, target.RemotePath, err)
	}

	for _, m := range resp.MediaContainer.Metadata {
		for _, file := range m.files() {
			if pathmap.Normalise(file) == target.RemotePath {
				return m.RatingKey, nil
			}
		}
	}

	return "", fmt.Errorf("%w: %s", ErrNoRatingKey, target.RemotePath)
}

func itemType(sectionType string) (string, bool) {
	switch sectionType {
	case SectionMovie:
		return typeMovie, true
	case SectionShow:
		return typeEpisode, true
	default:
		return "", false
	}
}

// NotifyPromoted runs after the source file is gone, so nothing it returns is a job
// failure; promote turns the error into a warning (plan.md 15.2).
func (c *Client) NotifyPromoted(ctx context.Context, localPath string) error {
	if !c.refreshAfter && !c.analyzeAfter {
		return nil
	}

	target, err := c.Resolve(ctx, localPath)
	if err != nil {
		return err
	}

	// Looked up first because the path stays stable (plan.md 16.2) and the refresh is
	// asynchronous, so a lookup straight after it races the scan.
	ratingKey, lookupErr := c.beforeRefreshRatingKey(ctx, target)

	if c.refreshAfter {
		if err := c.RefreshDir(ctx, target); err != nil {
			return err
		}
	}

	if !c.analyzeAfter {
		return nil
	}

	if ratingKey == "" {
		// A full job can change the extension (plan.md 6.1), so the pre-refresh lookup
		// misses on a path Plex has not indexed yet. One retry is all this is worth.
		if ratingKey, err = c.ratingKeyIn(ctx, target); err != nil {
			return errors.Join(lookupErr, err)
		}
	}

	return c.Analyze(ctx, ratingKey)
}

func (c *Client) beforeRefreshRatingKey(ctx context.Context, target Target) (string, error) {
	if !c.analyzeAfter {
		return "", nil
	}

	ratingKey, err := c.ratingKeyIn(ctx, target)
	if err != nil {
		c.log.DebugContext(ctx, "no plex item found for the promoted path yet",
			slog.String("path", target.RemotePath), slog.Any("error", err))

		return "", err
	}

	return ratingKey, nil
}
