package ingest

import (
	"path/filepath"
	"slices"
	"strings"
)

// MinSizeBytes is the 50 MB floor of plan.md 13.3. Below it a file is artwork,
// a theme tune or a stray extra, never something worth an encode.
const MinSizeBytes int64 = 50 << 20

// Exclusion is why a path is not a file Codarr processes. The empty value means
// it is.
type Exclusion string

// The hard-coded exclusions of plan.md 13.3, plus the per-file ignore list the
// UI writes to media_files.ignored.
const (
	NotExcluded       Exclusion = ""
	ExcludedExtrasDir Exclusion = "plex extras directory"
	ExcludedTrailer   Exclusion = "trailer"
	ExcludedSample    Exclusion = "sample"
	ExcludedPartial   Exclusion = "partial download"
	ExcludedHidden    Exclusion = "dotfile"
	ExcludedExtension Exclusion = "not a video extension"
	ExcludedTooSmall  Exclusion = "under 50 MB"
	ExcludedIgnored   Exclusion = "on the ignore list"
)

// ExtrasDirs are the Plex extras directories of plan.md 13.3. The whole subtree
// is pruned, not just its direct children.
func ExtrasDirs() []string {
	return []string{
		"Behind The Scenes", "Deleted Scenes", "Featurettes", "Interviews",
		"Scenes", "Shorts", "Trailers", "Other",
	}
}

// VideoExtensions is wider than the two containers Codarr writes (6.1), because a legacy
// container is exactly the thing that needs remuxing.
func VideoExtensions() []string {
	return []string{
		".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".asf", ".flv",
		".mpg", ".mpeg", ".m2v", ".ts", ".m2ts", ".mts", ".vob", ".ogm",
		".ogv", ".webm", ".divx", ".rm", ".rmvb", ".3gp", ".mxf",
	}
}

// PartialSuffixes are the in-progress download extensions of plan.md 13.3.
func PartialSuffixes() []string {
	return []string{".part", ".!qb", ".partial", ".tmp"}
}

// ExcludeDir compares case-insensitively: Plex creates these names, but a hand-made
// library is not always consistent about the capitals.
func ExcludeDir(name string) bool {
	for _, d := range ExtrasDirs() {
		if strings.EqualFold(name, d) {
			return true
		}
	}

	return false
}

// ExcludeFile applies the hard-coded file rules of plan.md 13.3, in the order
// that gives the most useful reason: what a path is beats how big it is.
func ExcludeFile(path string, sizeBytes int64) Exclusion {
	base := filepath.Base(path)
	lower := strings.ToLower(base)

	// Codarr's own staging files are .codarr-staging-* (15.1), so this rule is
	// what keeps a scan from picking up a transcode in flight.
	if strings.HasPrefix(base, ".") {
		return ExcludedHidden
	}

	if slices.Contains(PartialSuffixes(), strings.ToLower(filepath.Ext(base))) {
		return ExcludedPartial
	}

	stem := strings.TrimSuffix(lower, filepath.Ext(lower))

	switch {
	case strings.HasSuffix(stem, "-trailer"):
		return ExcludedTrailer
	case stem == "sample", strings.HasSuffix(stem, "-sample"):
		return ExcludedSample
	}

	if !slices.Contains(VideoExtensions(), strings.ToLower(filepath.Ext(base))) {
		return ExcludedExtension
	}

	if sizeBytes < MinSizeBytes {
		return ExcludedTooSmall
	}

	return NotExcluded
}
