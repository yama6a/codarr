package decide

import (
	"slices"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Snapshot is every policy constant the hash covers, exported so GET
// /api/policy (plan.md 20) renders the constants rather than a copy that drifts.
type Snapshot = policySnapshot

// AudioTarget is what one audio stream is converted to.
type AudioTarget = audioTarget

// Describe returns the policy in force, the same value the hash is computed
// from, so display and hash can never disagree.
func Describe() Snapshot { return currentPolicy() }

// AudioEncodeTarget is the conversion target for one stream, given the output
// container and the source channel count (plan.md 6.3).
func AudioEncodeTarget(c domain.Container, channels int) AudioTarget {
	return audioEncodeTarget(c, channels)
}

// AudioCopyCodecs is the copy list for a stream with that many channels.
func AudioCopyCodecs(channels int) []string { return slices.Clone(audioCopyList(channels)) }

// HardwareDecodeCodecs is the Gen 9.5 decode set of plan.md 10.1, deliberately
// outside the policy hash because it selects a decode path, not the output.
func HardwareDecodeCodecs() []string { return slices.Clone(hardwareDecodeCodecs) }

// TagKeys are the loop-prevention markers of plan.md 12; the tag alone never
// justifies a skip, see SkipCheck.
func TagKeys() []string { return []string{TagPresent, TagVersion, TagPolicy} }
