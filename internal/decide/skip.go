package decide

import (
	"fmt"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// The global tags every output carries, plan.md 12.
const (
	TagPresent = "CODARR"
	TagVersion = "CODARR_VERSION"
	TagPolicy  = "CODARR_POLICY"
)

// SkipCheck is the loop-prevention conjunction of plan.md 12. All three parts
// are reported, not just the verdict, because "tagged but modified" is the case
// that has to be visible in the UI rather than silently reprocessed.
type SkipCheck struct {
	Skip               bool
	Tagged             bool
	PolicyMatches      bool
	FingerprintMatches bool
	Provenance         domain.Provenance
	Reason             string
}

// CheckSkip answers "has Codarr already done this file, and is it still the
// file Codarr wrote?". The tag alone is never enough: mkvmerge carries global
// tags through a subtitle embed, so a re-tagged file with a fresh PGS track
// would otherwise be invisible forever.
func (e Engine) CheckSkip(probe *ffprobe.Result, currentFingerprint, recordedOutputFingerprint string) SkipCheck {
	c := SkipCheck{
		Provenance: domain.DeriveProvenance(recordedOutputFingerprint, currentFingerprint),
	}

	if probe != nil {
		_, c.Tagged = probe.Format.Tag(TagPresent)
	}

	policy, _ := tagOf(probe, TagPolicy)
	c.PolicyMatches = policy == PolicyHash()
	c.FingerprintMatches = recordedOutputFingerprint != "" &&
		currentFingerprint != "" &&
		recordedOutputFingerprint == currentFingerprint

	switch {
	case !c.Tagged:
		c.Reason = "no CODARR tag"
	case !c.PolicyMatches:
		c.Reason = fmt.Sprintf("tagged with policy %q, current policy is %q", policy, PolicyHash())
	case !c.FingerprintMatches:
		c.Reason = "tagged and on the current policy, but the file no longer matches the fingerprint Codarr recorded"
	default:
		c.Skip = true
		c.Reason = "tagged, on the current policy, and unchanged since Codarr wrote it"
	}

	return c
}

func tagOf(probe *ffprobe.Result, name string) (string, bool) {
	if probe == nil {
		return "", false
	}

	return probe.Format.Tag(name)
}
