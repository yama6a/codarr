// Package pathmap rewrites paths between Codarr's view of the filesystem and
// another service's view of it, and attributes a file to the *arr instance that
// owns it (plan.md 16.2).
//
// Paths are compared byte for byte and component by component. The library is
// on Linux, so "/Media" and "/media" are different directories.
package pathmap

import (
	"path"
	"sort"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Mapper rewrites paths in both directions for one service, longest prefix
// first so nested mappings resolve to the most specific one.
type Mapper struct {
	toLocal  []rule
	toRemote []rule
}

type rule struct {
	from string
	to   string
}

// New builds a Mapper from one service's mappings. Order in the slice does not
// matter; the sort key is prefix length, not the sort column.
func New(mappings []domain.PathMapping) *Mapper {
	m := &Mapper{
		toLocal:  make([]rule, 0, len(mappings)),
		toRemote: make([]rule, 0, len(mappings)),
	}

	for _, pm := range mappings {
		local, remote := Normalise(pm.Local), Normalise(pm.Remote)
		if local == "" || remote == "" {
			continue
		}

		m.toLocal = append(m.toLocal, rule{from: remote, to: local})
		m.toRemote = append(m.toRemote, rule{from: local, to: remote})
	}

	sortLongestFirst(m.toLocal)
	sortLongestFirst(m.toRemote)

	return m
}

// ToLocal rewrites a path as another service reports it into Codarr's view.
func (m *Mapper) ToLocal(remote string) (string, bool) { return apply(m.toLocal, remote) }

// ToRemote rewrites one of Codarr's paths into another service's view.
func (m *Mapper) ToRemote(local string) (string, bool) { return apply(m.toRemote, local) }

// Normalise is the single definition of how a path is compared: cleaned, with
// any trailing slash removed. It returns "" for anything that is not an
// absolute path, which callers treat as unmappable rather than as root.
func Normalise(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}

	return path.Clean(p)
}

// UnderPrefix reports whether p is prefix itself or lies beneath it. Both must
// already be normalised. It compares whole components, so /media never matches
// /media-archive.
func UnderPrefix(p, prefix string) bool {
	if p == prefix {
		return true
	}

	if prefix == "/" {
		return strings.HasPrefix(p, "/")
	}

	return strings.HasPrefix(p, prefix+"/")
}

func apply(rules []rule, p string) (string, bool) {
	norm := Normalise(p)
	if norm == "" {
		return p, false
	}

	for _, r := range rules {
		if !UnderPrefix(norm, r.from) {
			continue
		}

		if norm == r.from {
			return r.to, true
		}

		if r.from == "/" {
			return path.Join(r.to, norm), true
		}

		return r.to + norm[len(r.from):], true
	}

	return norm, false
}

func sortLongestFirst(rules []rule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if len(rules[i].from) != len(rules[j].from) {
			return len(rules[i].from) > len(rules[j].from)
		}

		return rules[i].from < rules[j].from
	})
}
