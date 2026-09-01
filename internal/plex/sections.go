package plex

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// Plex library section types, as they appear in GET /library/sections.
const (
	SectionMovie = "movie"
	SectionShow  = "show"
)

// Section is one library section and the directories it covers, which is the
// only thing that resolves a local path to a section id (plan.md 16.1).
type Section struct {
	Key       string
	Title     string
	Type      string
	Locations []string
}

// Sections returns the library sections, from cache when it is still warm.
func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	if cached, ok := c.sections.get(c.clk.Now()); ok {
		return cached, nil
	}

	return c.refreshSections(ctx)
}

// InvalidateSections drops the cached section list, for the UI's re-read after
// an operator has edited the libraries.
func (c *Client) InvalidateSections() { c.sections.clear() }

func (c *Client) refreshSections(ctx context.Context) ([]Section, error) {
	var resp sectionsResponse

	err := c.tr.do(ctx, request{method: http.MethodGet, path: "/library/sections", out: &resp})
	if err != nil {
		return nil, fmt.Errorf("plex: listing library sections failed: %w", err)
	}

	out := make([]Section, 0, len(resp.MediaContainer.Directory))

	for _, d := range resp.MediaContainer.Directory {
		s := Section{Key: d.Key, Title: d.Title, Type: d.Type, Locations: make([]string, 0, len(d.Location))}

		for _, l := range d.Location {
			if norm := pathmap.Normalise(l.Path); norm != "" {
				s.Locations = append(s.Locations, norm)
			}
		}

		out = append(out, s)
	}

	c.sections.set(out, c.clk.Now())

	return out, nil
}

// Target is one local path resolved onto the Plex server: which section covers
// it, and the directory to hand to the partial-scan verb.
type Target struct {
	Section    Section
	RemotePath string
	RemoteDir  string
}

// Resolve finds the section whose Location contains the path, longest prefix first so
// nested locations resolve to the most specific one.
func (c *Client) Resolve(ctx context.Context, localPath string) (Target, error) {
	norm := pathmap.Normalise(localPath)
	if norm == "" {
		return Target{}, fmt.Errorf("plex: %q is not an absolute path", localPath)
	}

	remote, _ := c.mapper.ToRemote(norm)

	sections, err := c.Sections(ctx)
	if err != nil {
		return Target{}, err
	}

	section, ok := sectionFor(sections, remote)
	if !ok {
		return Target{}, fmt.Errorf("%w: %s", ErrNoSection, remote)
	}

	return Target{Section: section, RemotePath: remote, RemoteDir: path.Dir(remote)}, nil
}

func sectionFor(sections []Section, remote string) (Section, bool) {
	var (
		best    Section
		bestLen = -1
		found   bool
	)

	for _, s := range sections {
		for _, loc := range s.Locations {
			if !pathmap.UnderPrefix(remote, loc) || len(loc) <= bestLen {
				continue
			}

			best, bestLen, found = s, len(loc), true
		}
	}

	return best, found
}

type sectionCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	sections []Section
	at       time.Time
}

func (s *sectionCache) get(now time.Time) ([]Section, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sections == nil || now.Sub(s.at) >= s.ttl {
		return nil, false
	}

	return s.sections, true
}

func (s *sectionCache) set(sections []Section, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sections, s.at = sections, now
}

func (s *sectionCache) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sections, s.at = nil, time.Time{}
}
