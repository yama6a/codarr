package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func taggedProbe(tags map[string]string) *ffprobe.Result {
	r := mkv(video("h264", "High"), audio("aac", 2))
	r.Format.Tags = tags

	return r
}

func TestEngine_CheckSkipIsAConjunction(t *testing.T) {
	t.Parallel()

	const (
		fingerprint = "xxh3-128:9f2c"
		other       = "xxh3-128:0000"
	)

	current := map[string]string{
		decide.TagPresent: "1",
		decide.TagVersion: "0.4.0",
		decide.TagPolicy:  decide.PolicyHash(),
	}

	tests := []struct {
		name        string
		probe       *ffprobe.Result
		current     string
		recorded    string
		skip        bool
		tagged      bool
		policy      bool
		fingerprint bool
		provenance  domain.Provenance
		reason      string
	}{
		{
			name:       "untagged file",
			probe:      taggedProbe(nil),
			current:    fingerprint,
			provenance: domain.ProvenanceUntouched,
			reason:     "no CODARR tag",
		},
		{
			name:        "tag, policy and fingerprint all agree",
			probe:       taggedProbe(current),
			current:     fingerprint,
			recorded:    fingerprint,
			skip:        true,
			tagged:      true,
			policy:      true,
			fingerprint: true,
			provenance:  domain.ProvenanceCodarrOutput,
			reason:      "tagged, on the current policy, and unchanged since Codarr wrote it",
		},
		{
			name:       "tag and policy match but the file was rewritten",
			probe:      taggedProbe(current),
			current:    other,
			recorded:   fingerprint,
			tagged:     true,
			policy:     true,
			provenance: domain.ProvenanceModified,
			reason:     "tagged and on the current policy, but the file no longer matches the fingerprint Codarr recorded",
		},
		{
			name: "tagged under an older policy",
			probe: taggedProbe(map[string]string{
				decide.TagPresent: "1",
				decide.TagPolicy:  "deadbeef",
			}),
			current:     fingerprint,
			recorded:    fingerprint,
			tagged:      true,
			fingerprint: true,
			provenance:  domain.ProvenanceCodarrOutput,
			reason:      `tagged with policy "deadbeef", current policy is "` + decide.PolicyHash() + `"`,
		},
		{
			name:       "tagged but Codarr has no record of writing it",
			probe:      taggedProbe(current),
			current:    fingerprint,
			tagged:     true,
			policy:     true,
			provenance: domain.ProvenanceUntouched,
			reason:     "tagged and on the current policy, but the file no longer matches the fingerprint Codarr recorded",
		},
		{
			name:        "tag present with no policy tag at all",
			probe:       taggedProbe(map[string]string{decide.TagPresent: "1"}),
			current:     fingerprint,
			recorded:    fingerprint,
			tagged:      true,
			fingerprint: true,
			provenance:  domain.ProvenanceCodarrOutput,
			reason:      `tagged with policy "", current policy is "` + decide.PolicyHash() + `"`,
		},
		{
			name:       "no fingerprint computed yet",
			probe:      taggedProbe(current),
			recorded:   fingerprint,
			tagged:     true,
			policy:     true,
			provenance: domain.ProvenanceModified,
			reason:     "tagged and on the current policy, but the file no longer matches the fingerprint Codarr recorded",
		},
		{
			name:        "no probe",
			current:     fingerprint,
			recorded:    fingerprint,
			fingerprint: true,
			provenance:  domain.ProvenanceCodarrOutput,
			reason:      "no CODARR tag",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := decide.New().CheckSkip(tc.probe, tc.current, tc.recorded)
			require.Equal(t, decide.SkipCheck{
				Skip:               tc.skip,
				Tagged:             tc.tagged,
				PolicyMatches:      tc.policy,
				FingerprintMatches: tc.fingerprint,
				Provenance:         tc.provenance,
				Reason:             tc.reason,
			}, got)
		})
	}
}

func TestEngine_CheckSkipReadsLowercaseTags(t *testing.T) {
	t.Parallel()

	probe := taggedProbe(map[string]string{
		"codarr":        "1",
		"codarr_policy": decide.PolicyHash(),
	})

	got := decide.New().CheckSkip(probe, "fp", "fp")
	require.True(t, got.Skip, "MP4 udta tags do not come back in the case they were written")
}
