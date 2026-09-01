package pathmap

import (
	"sort"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Conflict is two or more enabled *arr instances claiming the same root path, which
// plan.md 16.2 says to show and process without notifying, never to guess at.
type Conflict struct {
	Path        string
	InstanceIDs []int64
}

// Attribution is the owner of one file. ArrInstanceID is nil when the matched
// root has no instance, or when Conflict is set.
type Attribution struct {
	Root          domain.Root
	ArrInstanceID *int64
	Conflict      *Conflict
}

// ImportedRoot is one root folder an *arr reported, mapped into Codarr's view.
//
// All four live instances report the literal path "/media" (VERIFY.md), so mapping has
// to happen before the row exists or attribution is impossible.
type ImportedRoot struct {
	ArrInstanceID int64
	ReportedPath  string
	Path          string
	Mapped        bool
}

// Root is the roots row this candidate becomes.
func (r ImportedRoot) Root() domain.Root {
	id := r.ArrInstanceID

	return domain.Root{
		Path:          r.Path,
		ArrInstanceID: &id,
		Imported:      true,
		Enabled:       true,
	}
}

// ImportRoots maps reported root folders through the instance's own mappings; Mapped
// false means the instance is missing its mapping and is about to collide.
func ImportRoots(m *Mapper, instanceID int64, reported []string) []ImportedRoot {
	out := make([]ImportedRoot, 0, len(reported))

	for _, remote := range reported {
		norm := Normalise(remote)
		if norm == "" {
			continue
		}

		local, mapped := m.ToLocal(norm)

		out = append(out, ImportedRoot{
			ArrInstanceID: instanceID,
			ReportedPath:  norm,
			Path:          local,
			Mapped:        mapped,
		})
	}

	return out
}

// Conflicts lists every path claimed by more than one enabled instance, taking plain
// roots so it also serves import candidates that have not been written yet.
func Conflicts(roots []domain.Root) []Conflict {
	byPath := map[string][]int64{}

	for _, r := range roots {
		norm := Normalise(r.Path)
		if norm == "" || !r.Enabled || r.ArrInstanceID == nil {
			continue
		}

		byPath[norm] = appendDistinct(byPath[norm], *r.ArrInstanceID)
	}

	out := []Conflict{}

	for p, ids := range byPath {
		if len(ids) < 2 {
			continue
		}

		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		out = append(out, Conflict{Path: p, InstanceIDs: ids})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	return out
}

// Attribute picks the enabled root with the longest matching prefix (plan.md 16.2), so
// nested roots resolve to the inner one, and reports false when no root matches.
func Attribute(roots []domain.Root, filePath string) (Attribution, bool) {
	norm := Normalise(filePath)
	if norm == "" {
		return Attribution{}, false
	}

	best := longestPrefixMatches(roots, norm)
	if len(best) == 0 {
		return Attribution{}, false
	}

	ids := distinctInstances(best)
	att := Attribution{Root: best[0]}

	switch len(ids) {
	case 0:
	case 1:
		att.ArrInstanceID = &ids[0]
		att.Root = rootFor(best, ids[0])
	default:
		att.Conflict = &Conflict{Path: Normalise(best[0].Path), InstanceIDs: ids}
	}

	return att, true
}

func longestPrefixMatches(roots []domain.Root, filePath string) []domain.Root {
	best := make([]domain.Root, 0, len(roots))
	bestLen := -1

	for _, r := range roots {
		root := Normalise(r.Path)
		if root == "" || !r.Enabled || !UnderPrefix(filePath, root) {
			continue
		}

		if len(root) > bestLen {
			best, bestLen = best[:0], len(root)
		}

		if len(root) == bestLen {
			best = append(best, r)
		}
	}

	sort.SliceStable(best, func(i, j int) bool { return best[i].ID < best[j].ID })

	return best
}

func distinctInstances(roots []domain.Root) []int64 {
	var ids []int64

	for _, r := range roots {
		if r.ArrInstanceID != nil {
			ids = appendDistinct(ids, *r.ArrInstanceID)
		}
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return ids
}

func rootFor(roots []domain.Root, instanceID int64) domain.Root {
	for _, r := range roots {
		if r.ArrInstanceID != nil && *r.ArrInstanceID == instanceID {
			return r
		}
	}

	return roots[0]
}

func appendDistinct(ids []int64, id int64) []int64 {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}

	return append(ids, id)
}
