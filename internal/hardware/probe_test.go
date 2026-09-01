package hardware_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/hardware/mock"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

const versionOutput = "ffmpeg version 7.1.4-Jellyfin Copyright (c) 2000-2025 the FFmpeg developers\n" +
	"built with gcc 12\n"

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// script routes a probe invocation to a canned answer keyed on what the args
// say the run is for, so a test states the matrix rather than a call order.
type script struct {
	version string
	versErr error
	encode  map[string]error
	sample  error
	decode  map[string]error
}

func (s script) runner(t *testing.T) *mock.RunnerMock {
	t.Helper()

	return &mock.RunnerMock{
		RunFunc: func(_ context.Context, args []string) (string, error) {
			switch {
			case slices.Contains(args, "-version"):
				return s.version, s.versErr
			case slices.Contains(args, "libvpx-vp9"):
				return "vp9 sample", s.sample
			case slices.Contains(args, "-hwaccel"):
				return "decode failed", s.decode[argAfter(args, "-hwaccel")]
			default:
				return "encode failed", s.encode[argAfter(args, "-c:v")+":"+argAfter(args, "-profile:v")]
			}
		},
	}
}

func argAfter(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}

	return args[i+1]
}

func newProber(t *testing.T, s script, st *mock.StoreMock, fs *mock.FSMock) *hardware.Prober {
	t.Helper()

	return hardware.New(s.runner(t), st, fs, clock.NewFake(probedAt), device, "/tmp", discard())
}

func emptyStore() *mock.StoreMock {
	return &mock.StoreMock{
		ListHWCapabilitiesFunc: func(context.Context) ([]domain.HWCapability, error) {
			return nil, nil
		},
		ReplaceHWCapabilitiesFunc: func(context.Context, []domain.HWCapability) error { return nil },
	}
}

func okFS() *mock.FSMock {
	return &mock.FSMock{RemoveFunc: func(string) error { return nil }}
}

func TestProber_ProbeWritesTheWholeMatrix(t *testing.T) {
	t.Parallel()

	st, fs := emptyStore(), okFS()
	caps, err := newProber(t, script{version: versionOutput}, st, fs).Probe(t.Context())
	require.NoError(t, err)

	require.Equal(t, hardware.Capabilities{
		Device:        device,
		FfmpegVersion: "7.1.4-Jellyfin",
		ProbedAt:      probedAt,
		Entries: []domain.HWCapability{
			encodeEntry("qsv", "main", true, ""),
			encodeEntry("qsv", "main10", true, ""),
			encodeEntry("vaapi", "main", true, ""),
			encodeEntry("vaapi", "main10", true, ""),
			decodeEntry("qsv", true, ""),
			decodeEntry("vaapi", true, ""),
		},
	}, caps)

	require.Len(t, st.ReplaceHWCapabilitiesCalls(), 1)
	require.Equal(t, caps.Entries, st.ReplaceHWCapabilitiesCalls()[0].Caps)
	require.Len(t, fs.RemoveCalls(), 1)
	require.Equal(t, "/tmp/.codarr-vp9-probe.webm", fs.RemoveCalls()[0].Path)
}

func TestProber_ProbeRecordsPerProfileFailuresSeparately(t *testing.T) {
	t.Parallel()

	// plan.md 10.1: 10-bit is tested separately from 8-bit precisely so this
	// case is distinguishable.
	s := script{
		version: versionOutput,
		encode: map[string]error{
			"hevc_qsv:main10": errors.New("exit status 1"),
		},
	}

	caps, err := newProber(t, s, emptyStore(), okFS()).Probe(t.Context())
	require.NoError(t, err)

	require.True(t, caps.Encodes(hardware.BackendQSV, hardware.ProfileMain))
	require.False(t, caps.Encodes(hardware.BackendQSV, hardware.ProfileMain10))
	require.Contains(t, caps.Remediation(), "driver stack rather than the hardware")
}

func TestProber_ProbeStoresTheStderrOfAFailedEncode(t *testing.T) {
	t.Parallel()

	s := script{
		version: versionOutput,
		encode: map[string]error{
			"hevc_vaapi:main": errors.New("exit status 1"),
		},
	}

	caps, err := newProber(t, s, emptyStore(), okFS()).Probe(t.Context())
	require.NoError(t, err)

	require.Equal(t, encodeEntry("vaapi", "main", false, "encode failed"), caps.Entries[2])
}

func TestProber_ProbeMarksVP9InconclusiveWhenTheSampleCannotBeMade(t *testing.T) {
	t.Parallel()

	s := script{version: versionOutput, sample: errors.New("Unknown encoder 'libvpx-vp9'")}

	fs := okFS()
	caps, err := newProber(t, s, emptyStore(), fs).Probe(t.Context())
	require.NoError(t, err)

	require.False(t, caps.DecodesVP9(hardware.BackendQSV))
	require.Equal(t, decodeEntry("qsv", false,
		"inconclusive: could not synthesise a VP9 sample to decode: vp9 sample"), caps.Entries[4])
	require.Empty(t, fs.RemoveCalls(), "nothing was written, so nothing is removed")
}

func TestProber_ProbeRecordsAFailedVP9Decode(t *testing.T) {
	t.Parallel()

	s := script{
		version: versionOutput,
		decode:  map[string]error{"qsv": errors.New("exit status 1")},
	}

	caps, err := newProber(t, s, emptyStore(), okFS()).Probe(t.Context())
	require.NoError(t, err)

	require.False(t, caps.DecodesVP9(hardware.BackendQSV))
	require.True(t, caps.DecodesVP9(hardware.BackendVAAPI))
	require.Contains(t, caps.Remediation(), "qsv did not decode VP9: decode failed")
}

func TestProber_ProbeFailsWhenFfmpegIsNotRunnable(t *testing.T) {
	t.Parallel()

	s := script{versErr: errors.New("exec: \"ffmpeg\": executable file not found in $PATH")}

	_, err := newProber(t, s, emptyStore(), okFS()).Probe(t.Context())
	require.ErrorIs(t, err, hardware.ErrNoFfmpeg)
}

func TestProber_ProbeFailsOnUnrecognisedVersionOutput(t *testing.T) {
	t.Parallel()

	_, err := newProber(t, script{version: "not ffmpeg at all"}, emptyStore(), okFS()).
		Probe(t.Context())
	require.ErrorIs(t, err, hardware.ErrNoFfmpeg)
}

func TestProber_ProbeSurfacesACacheWriteFailure(t *testing.T) {
	t.Parallel()

	st := emptyStore()
	st.ReplaceHWCapabilitiesFunc = func(context.Context, []domain.HWCapability) error {
		return errors.New("disk full")
	}

	_, err := newProber(t, script{version: versionOutput}, st, okFS()).Probe(t.Context())
	require.ErrorContains(t, err, "cache capabilities: disk full")
}

func TestProber_CapabilitiesServesTheCacheForTheSameFfmpegBuild(t *testing.T) {
	t.Parallel()

	cached := everythingWorks().Entries
	st := emptyStore()
	st.ListHWCapabilitiesFunc = func(context.Context) ([]domain.HWCapability, error) {
		return cached, nil
	}

	runner := script{version: versionOutput}.runner(t)
	caps, err := hardware.New(runner, st, okFS(), clock.NewFake(probedAt), device, "/tmp", discard()).
		Capabilities(t.Context())
	require.NoError(t, err)

	require.Equal(t, everythingWorks(), caps)
	require.Empty(t, st.ReplaceHWCapabilitiesCalls())
	require.Len(t, runner.RunCalls(), 1, "only the version query")
}

func TestProber_CapabilitiesReprobesWhenTheFfmpegVersionChanged(t *testing.T) {
	t.Parallel()

	stale := everythingWorks().Entries
	for i := range stale {
		stale[i].FfmpegVersion = "6.0"
	}

	st := emptyStore()
	st.ListHWCapabilitiesFunc = func(context.Context) ([]domain.HWCapability, error) {
		return stale, nil
	}

	caps, err := newProber(t, script{version: versionOutput}, st, okFS()).Capabilities(t.Context())
	require.NoError(t, err)

	require.Equal(t, "7.1.4-Jellyfin", caps.FfmpegVersion)
	require.Len(t, st.ReplaceHWCapabilitiesCalls(), 1)
}

func TestProber_CapabilitiesReprobesOnAHalfWrittenCache(t *testing.T) {
	t.Parallel()

	mixed := everythingWorks().Entries
	mixed[3].FfmpegVersion = "6.0"

	st := emptyStore()
	st.ListHWCapabilitiesFunc = func(context.Context) ([]domain.HWCapability, error) {
		return mixed, nil
	}

	_, err := newProber(t, script{version: versionOutput}, st, okFS()).Capabilities(t.Context())
	require.NoError(t, err)
	require.Len(t, st.ReplaceHWCapabilitiesCalls(), 1)
}

func TestProber_CapabilitiesUsesTheNewestProbedAtFromTheCache(t *testing.T) {
	t.Parallel()

	later := probedAt.Add(time.Hour)
	cached := everythingWorks().Entries
	cached[5].ProbedAt = later

	st := emptyStore()
	st.ListHWCapabilitiesFunc = func(context.Context) ([]domain.HWCapability, error) {
		return cached, nil
	}

	caps, err := newProber(t, script{version: versionOutput}, st, okFS()).Capabilities(t.Context())
	require.NoError(t, err)
	require.Equal(t, later, caps.ProbedAt)
}

func TestProber_CapabilitiesSurfacesACacheReadFailure(t *testing.T) {
	t.Parallel()

	st := emptyStore()
	st.ListHWCapabilitiesFunc = func(context.Context) ([]domain.HWCapability, error) {
		return nil, errors.New("database is locked")
	}

	_, err := newProber(t, script{version: versionOutput}, st, okFS()).Capabilities(t.Context())
	require.ErrorContains(t, err, "read cached capabilities: database is locked")
}

func TestProber_ProbeToleratesAnUnremovableSample(t *testing.T) {
	t.Parallel()

	fs := &mock.FSMock{RemoveFunc: func(string) error { return errors.New("read-only") }}

	caps, err := newProber(t, script{version: versionOutput}, emptyStore(), fs).Probe(t.Context())
	require.NoError(t, err)
	require.True(t, caps.DecodesVP9(hardware.BackendQSV))
}

func TestParseVersion_ReadsTheJellyfinBuild(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"jellyfin", versionOutput, "7.1.4-Jellyfin"},
		{"debian", "ffmpeg version 5.1.6-0+deb12u1 Copyright", "5.1.6-0+deb12u1"},
		{"git build", "ffmpeg version N-118273-g1c2d3e4", "N-118273-g1c2d3e4"},
		{"empty", "", ""},
		{"no version token", "ffmpeg 7.1.4", ""},
		{"trailing version token", "ffmpeg version", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, hardware.ParseVersion(tc.in))
		})
	}
}

func TestProber_ProbeFallsBackToTheGoErrorWhenFfmpegPrintedNothing(t *testing.T) {
	t.Parallel()

	runner := &mock.RunnerMock{
		RunFunc: func(_ context.Context, args []string) (string, error) {
			if slices.Contains(args, "-version") {
				return versionOutput, nil
			}

			return "", errors.New("signal: killed")
		},
	}

	caps, err := hardware.New(runner, emptyStore(), okFS(), clock.NewFake(probedAt),
		device, "/tmp", discard()).Probe(t.Context())
	require.NoError(t, err)

	for _, e := range caps.Entries {
		require.False(t, e.Works)
		require.Contains(t, e.Error, "signal: killed")
	}
}

func TestProber_ProbeTruncatesAChattyDriversOutput(t *testing.T) {
	t.Parallel()

	noisy := strings.Repeat("libva error line\n", 200) + "END"

	runner := &mock.RunnerMock{
		RunFunc: func(_ context.Context, args []string) (string, error) {
			if slices.Contains(args, "-version") {
				return versionOutput, nil
			}

			return noisy, errors.New("exit status 1")
		},
	}

	caps, err := hardware.New(runner, emptyStore(), okFS(), clock.NewFake(probedAt),
		device, "/tmp", discard()).Probe(t.Context())
	require.NoError(t, err)

	require.LessOrEqual(t, len(caps.Entries[0].Error), 512)
	require.True(t, strings.HasSuffix(caps.Entries[0].Error, "END"))
	require.NotContains(t, caps.Entries[0].Error, "\n")
}
