package decide

import (
	"slices"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Snapshot is every policy constant the hash covers, in one value. It is
// exported so GET /api/policy (plan.md 20) renders the constants themselves
// rather than a second copy of them that can drift.
type Snapshot = policySnapshot

// AudioTarget is what one audio stream is converted to.
type AudioTarget = audioTarget

// Describe returns the policy in force. The same value the hash is computed
// from, so the display and the hash can never disagree.
func Describe() Snapshot { return currentPolicy() }

// AudioEncodeTarget is the conversion target for one stream, given the output
// container and the source channel count (plan.md 6.3).
func AudioEncodeTarget(c domain.Container, channels int) AudioTarget {
	return audioEncodeTarget(c, channels)
}

// AudioCopyCodecs is the copy list for a stream with that many channels.
func AudioCopyCodecs(channels int) []string { return slices.Clone(audioCopyList(channels)) }

// HardwareDecodeCodecs is the Gen 9.5 decode set of plan.md 10.1. It is
// deliberately outside the policy hash: it selects a decode path, not what ends
// up in the output.
func HardwareDecodeCodecs() []string { return slices.Clone(hardwareDecodeCodecs) }

// TagKeys are the loop-prevention markers written into every output (plan.md
// 12). The tag alone never justifies a skip; the rule is a conjunction with the
// policy hash and the recorded output fingerprint.
func TagKeys() []string { return []string{TagPresent, TagVersion, TagPolicy} }
