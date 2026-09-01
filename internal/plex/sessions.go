package plex

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// Session is one thing Plex is playing right now, with the local paths it is
// reading resolved.
type Session struct {
	RatingKey   string
	Title       string
	User        string
	Player      string
	Transcoding bool
	LocalPaths  []string
}

// Describe is the human-readable form that ends up in jobs.blocked_by: who is
// watching what, so the reason a promotion is deferred is legible without
// opening the logs (plan.md 19.1).
func (s Session) Describe() string {
	who := s.User
	if who == "" {
		who = "someone"
	}

	what := s.Title
	if what == "" {
		what = "an item"
	}

	desc := who + " is watching " + what

	if s.Player != "" {
		desc += " on " + s.Player
	}

	if s.Transcoding {
		desc += " (transcoding)"
	}

	return desc
}

// Sessions lists what is playing. The listing itself is never cached: the
// guard's last check runs immediately before the rename and a cached answer
// there would reopen the race it exists to close (plan.md 15.6). Only the
// rating-key-to-file lookups behind it are cached.
func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var resp metadataResponse

	if err := c.tr.do(ctx, request{method: http.MethodGet, path: "/status/sessions", out: &resp}); err != nil {
		return nil, fmt.Errorf("plex: listing active sessions failed: %w", err)
	}

	out := make([]Session, 0, len(resp.MediaContainer.Metadata))

	for _, m := range resp.MediaContainer.Metadata {
		paths, err := c.sessionPaths(ctx, m)
		if err != nil {
			return nil, err
		}

		out = append(out, Session{
			RatingKey:   m.RatingKey,
			Title:       sessionTitle(m),
			User:        m.User.Title,
			Player:      playerName(m),
			Transcoding: m.transcoding(),
			LocalPaths:  paths,
		})
	}

	return out, nil
}

// sessionPaths resolves what a session is reading. plan.md 16.1: a direct-play
// session's Part carries the file attribute and a transcoding one does not, so
// the item is fetched by rating key whenever the session itself is silent.
func (c *Client) sessionPaths(ctx context.Context, m metadata) ([]string, error) {
	remote := m.files()

	if len(remote) == 0 && m.RatingKey != "" {
		var err error
		if remote, err = c.partsFor(ctx, m.RatingKey); err != nil {
			return nil, err
		}
	}

	out := make([]string, 0, len(remote))

	for _, r := range remote {
		local, _ := c.mapper.ToLocal(r)
		if local = pathmap.Normalise(local); local != "" {
			out = append(out, local)
		}
	}

	return out, nil
}

func (c *Client) partsFor(ctx context.Context, ratingKey string) ([]string, error) {
	if cached, ok := c.parts.get(ratingKey, c.clk.Now()); ok {
		return cached, nil
	}

	var resp metadataResponse

	err := c.tr.do(ctx, request{
		method: http.MethodGet,
		path:   "/library/metadata/" + ratingKey,
		out:    &resp,
	})
	if err != nil {
		return nil, fmt.Errorf("plex: reading item %s failed: %w", ratingKey, err)
	}

	var files []string
	for _, m := range resp.MediaContainer.Metadata {
		files = append(files, m.files()...)
	}

	c.parts.set(ratingKey, files, c.clk.Now())

	return files, nil
}

// IsStreaming implements promote.StreamGuard. plan.md rule 7: a file being
// streamed is deferred, never skipped, and never replaced under the reader.
func (c *Client) IsStreaming(ctx context.Context, path string) (bool, string, error) {
	target := pathmap.Normalise(path)
	if target == "" {
		return false, "", fmt.Errorf("plex: %q is not an absolute path", path)
	}

	sessions, err := c.Sessions(ctx)
	if err != nil {
		return false, "", err
	}

	for _, s := range sessions {
		for _, p := range s.LocalPaths {
			if p == target {
				return true, s.Describe(), nil
			}
		}
	}

	return false, "", nil
}

func sessionTitle(m metadata) string {
	if m.GrandparentTitle == "" {
		return m.Title
	}

	parts := []string{m.GrandparentTitle}
	if m.ParentIndex > 0 && m.Index > 0 {
		parts = append(parts, fmt.Sprintf("S%02dE%02d", m.ParentIndex, m.Index))
	}

	if m.Title != "" {
		parts = append(parts, m.Title)
	}

	return strings.Join(parts, " - ")
}

func playerName(m metadata) string {
	switch {
	case m.Player.Title != "" && m.Player.Product != "":
		return m.Player.Title + " (" + m.Player.Product + ")"
	case m.Player.Title != "":
		return m.Player.Title
	default:
		return m.Player.Product
	}
}

// partCache holds a rating key's file paths for a short while. plan.md 16.1
// asks for a brief cache; the TTL is short because a `full` job can change a
// file's extension, which changes the path the key resolves to.
type partCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]partEntry
}

type partEntry struct {
	files []string
	at    time.Time
}

func (p *partCache) get(key string, now time.Time) ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	e, ok := p.entries[key]
	if !ok || now.Sub(e.at) >= p.ttl {
		return nil, false
	}

	return e.files, true
}

func (p *partCache) set(key string, files []string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.entries[key] = partEntry{files: files, at: now}
}
