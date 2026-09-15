package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestKindOf_CanonicalOrderAndDedupe(t *testing.T) {
	t.Parallel()

	k := domain.KindOf(domain.LabelRemux, domain.LabelVideo, domain.LabelRemux, domain.LabelAudio, "bogus")

	require.Equal(t, domain.Kind("video|audio|remux"), k)
	require.Equal(t, []domain.Label{domain.LabelVideo, domain.LabelAudio, domain.LabelRemux}, k.Labels())
	require.Equal(t, domain.KindSkip, domain.KindOf())
	require.Nil(t, domain.KindSkip.Labels())
}

func TestKind_HasMatchesWholeLabelsOnly(t *testing.T) {
	t.Parallel()

	k := domain.KindOf(domain.LabelSubtitles)

	require.True(t, k.Has(domain.LabelSubtitles))
	require.False(t, k.Has(domain.LabelVideo))
	require.False(t, k.Has("sub"))
	require.False(t, domain.KindSkip.Has(domain.LabelVideo))
}

func TestKind_Display(t *testing.T) {
	t.Parallel()

	require.Equal(t, "skip", domain.KindSkip.Display())
	require.True(t, domain.KindSkip.Skip())
	require.Equal(t, "audio|subtitles", domain.KindOf(domain.LabelAudio, domain.LabelSubtitles).Display())
	require.False(t, domain.KindOf(domain.LabelAudio).Skip())
}

func TestPriorityFor_QuickWinsFirstOnlyWhenEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		kind  domain.Kind
		quick bool
		want  int
	}{
		{name: "video encode sinks", kind: domain.KindOf(domain.LabelVideo, domain.LabelAudio), quick: true, want: domain.PriorityFull},
		{name: "subtitle work floats", kind: domain.KindOf(domain.LabelSubtitles), quick: true, want: domain.PriorityQuick},
		{name: "remux floats", kind: domain.KindOf(domain.LabelRemux), quick: true, want: domain.PriorityQuick},
		{name: "skip is normal", kind: domain.KindSkip, quick: true, want: domain.PriorityNormal},
		{name: "setting off flattens video", kind: domain.KindOf(domain.LabelVideo), quick: false, want: domain.PriorityNormal},
		{name: "setting off flattens audio", kind: domain.KindOf(domain.LabelAudio), quick: false, want: domain.PriorityNormal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, domain.PriorityFor(tc.kind, tc.quick))
		})
	}
}

func TestCountKinds_LabelsOverlapAndSkipIsDisjoint(t *testing.T) {
	t.Parallel()

	counts := domain.CountKinds(map[domain.Kind]int{
		domain.KindSkip: 5,
		domain.KindOf(domain.LabelVideo, domain.LabelAudio):     2,
		domain.KindOf(domain.LabelAudio, domain.LabelSubtitles): 3,
		domain.KindOf(domain.LabelRemux):                        1,
	})

	require.Equal(t, domain.KindCounts{Skip: 5, Video: 2, Audio: 5, Subtitles: 3, Remux: 1}, counts)

	var c domain.KindCounts
	c.Add(domain.KindOf(domain.LabelSubtitles))
	c.Add(domain.KindSkip)
	require.Equal(t, domain.KindCounts{Skip: 1, Subtitles: 1}, c)
}
