package domain

import (
	"slices"
	"strings"
)

// Label names one kind of work a plan carries (plan.md 7).
type Label string

// The labels, in the order a Kind lists them.
const (
	LabelVideo     Label = "video"
	LabelAudio     Label = "audio"
	LabelSubtitles Label = "subtitles"
	LabelRemux     Label = "remux"
)

// Labels returns every label in canonical order.
func Labels() []Label {
	return []Label{LabelVideo, LabelAudio, LabelSubtitles, LabelRemux}
}

// Kind is the set of labels a plan carries, pipe-joined in canonical order.
// Empty means every stream is already compatible.
type Kind string

// KindSkip is the plan that needs no write.
const KindSkip Kind = ""

const kindSeparator = "|"

// KindOf builds a Kind from any labels, in canonical order, ignoring
// duplicates and unknown values.
func KindOf(labels ...Label) Kind {
	parts := make([]string, 0, len(labels))

	for _, l := range Labels() {
		if slices.Contains(labels, l) {
			parts = append(parts, string(l))
		}
	}

	return Kind(strings.Join(parts, kindSeparator))
}

// Has reports whether the label is part of the set.
func (k Kind) Has(l Label) bool {
	return slices.Contains(k.Labels(), l)
}

// Labels returns the set in canonical order, nil for KindSkip.
func (k Kind) Labels() []Label {
	if k == KindSkip {
		return nil
	}

	parts := strings.Split(string(k), kindSeparator)
	out := make([]Label, 0, len(parts))

	for _, p := range parts {
		out = append(out, Label(p))
	}

	return out
}

// Skip reports whether the plan needs no write.
func (k Kind) Skip() bool { return k == KindSkip }

// Display is the Kind for messages, where an empty string reads as a bug.
func (k Kind) Display() string {
	if k.Skip() {
		return "skip"
	}

	return string(k)
}

// PriorityFor is plan.md 19's ordering: lower runs first, and with the setting
// on, plans without a video encode go ahead of the ones with one.
func PriorityFor(k Kind, prioritiseQuick bool) int {
	switch {
	case !prioritiseQuick, k.Skip():
		return PriorityNormal
	case k.Has(LabelVideo):
		return PriorityFull
	default:
		return PriorityQuick
	}
}

// KindCounts is how many plans carry each label. A plan counts under every
// label it has, so the label counts overlap; Skip is disjoint.
type KindCounts struct {
	Skip      int
	Video     int
	Audio     int
	Subtitles int
	Remux     int
}

// Add counts one plan.
func (c *KindCounts) Add(k Kind) {
	c.AddN(k, 1)
}

// AddN counts n plans of the same kind.
func (c *KindCounts) AddN(k Kind, n int) {
	if k.Skip() {
		c.Skip += n

		return
	}

	for _, l := range k.Labels() {
		switch l {
		case LabelVideo:
			c.Video += n
		case LabelAudio:
			c.Audio += n
		case LabelSubtitles:
			c.Subtitles += n
		case LabelRemux:
			c.Remux += n
		}
	}
}

// CountKinds folds a per-kind histogram into per-label counts.
func CountKinds(byKind map[Kind]int) KindCounts {
	var c KindCounts

	for k, n := range byKind {
		c.AddN(k, n)
	}

	return c
}
