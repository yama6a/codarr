package hardware_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

var probedAt = time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)

func encodeEntry(backend string, profile string, works bool, errText string) domain.HWCapability {
	return domain.HWCapability{
		Backend:       backend,
		Codec:         "hevc",
		Profile:       profile,
		Direction:     "encode",
		Works:         works,
		Error:         errText,
		FfmpegVersion: "7.1.4-Jellyfin",
		ProbedAt:      probedAt,
	}
}

func decodeEntry(backend string, works bool, errText string) domain.HWCapability {
	return domain.HWCapability{
		Backend:       backend,
		Codec:         "vp9",
		Direction:     "decode",
		Works:         works,
		Error:         errText,
		FfmpegVersion: "7.1.4-Jellyfin",
		ProbedAt:      probedAt,
	}
}

// everythingWorks is the expected UHD 630 result of plan.md 10.1.
func everythingWorks() hardware.Capabilities {
	return hardware.Capabilities{
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
	}
}

func TestCapabilities_SelectPrefersQSV(t *testing.T) {
	t.Parallel()

	require.Equal(t, hardware.Selection{
		Encoder: domain.EncoderQSV,
		Profile: hardware.ProfileMain10,
	}, everythingWorks().Select(true))
}

func TestCapabilities_SelectFallsBackToVAAPIPerProfile(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", true, ""),
		encodeEntry("qsv", "main10", false, "profile not supported"),
		encodeEntry("vaapi", "main", true, ""),
		encodeEntry("vaapi", "main10", true, ""),
	}}

	require.Equal(t, hardware.Selection{
		Encoder: domain.EncoderQSV,
		Profile: hardware.ProfileMain,
	}, caps.Select(false))

	require.Equal(t, hardware.Selection{
		Encoder:  domain.EncoderVAAPI,
		Profile:  hardware.ProfileMain10,
		FellBack: true,
		Reason:   "QSV does not encode HEVC main10 on this host",
	}, caps.Select(true))
}

func TestCapabilities_SelectFallsBackToSoftware(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", false, "no device"),
		encodeEntry("qsv", "main10", false, "no device"),
		encodeEntry("vaapi", "main", false, "no device"),
		encodeEntry("vaapi", "main10", false, "no device"),
	}}

	require.Equal(t, hardware.Selection{
		Encoder:  domain.EncoderSoftware,
		Profile:  hardware.ProfileMain,
		FellBack: true,
		Software: true,
		Reason:   "neither QSV nor VAAPI encodes HEVC main on this host; libx265 is many times slower",
	}, caps.Select(false))
}

func TestCapabilities_SelectOnAnUnprobedSetSaysSo(t *testing.T) {
	t.Parallel()

	require.Equal(t, hardware.Selection{
		Encoder:  domain.EncoderSoftware,
		Profile:  hardware.ProfileMain,
		FellBack: true,
		Software: true,
		Reason: "hardware has not been probed, so no accelerated encoder can be trusted " +
			"for HEVC main; libx265 is many times slower",
	}, hardware.Capabilities{}.Select(false))
}

func TestCapabilities_NextWalksTheChainDown(t *testing.T) {
	t.Parallel()

	caps := everythingWorks()

	next, ok := caps.Next(domain.EncoderQSV, false)
	require.True(t, ok)
	require.Equal(t, hardware.Selection{
		Encoder:  domain.EncoderVAAPI,
		Profile:  hardware.ProfileMain,
		FellBack: true,
		Reason:   "hevc_qsv failed at runtime",
	}, next)

	next, ok = caps.Next(domain.EncoderVAAPI, false)
	require.True(t, ok)
	require.Equal(t, hardware.Selection{
		Encoder:  domain.EncoderSoftware,
		Profile:  hardware.ProfileMain,
		FellBack: true,
		Software: true,
		Reason:   "hevc_vaapi failed at runtime and no other hardware encoder works",
	}, next)

	_, ok = caps.Next(domain.EncoderSoftware, false)
	require.False(t, ok)
}

func TestCapabilities_NextSkipsVAAPIWhenItDoesNotWork(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", true, ""),
		encodeEntry("vaapi", "main", false, "no device"),
	}}

	next, ok := caps.Next(domain.EncoderQSV, false)
	require.True(t, ok)
	require.Equal(t, hardware.Selection{
		Encoder:  domain.EncoderSoftware,
		Profile:  hardware.ProfileMain,
		FellBack: true,
		Software: true,
		Reason:   "hevc_qsv failed at runtime and no other hardware encoder works",
	}, next)
}

func TestCapabilities_DecodePathFollowsTheGen95Set(t *testing.T) {
	t.Parallel()

	caps := everythingWorks()

	for _, codec := range hardware.DecodeCodecs() {
		require.Equal(t, domain.DecodeHardware, caps.DecodePath(domain.EncoderQSV, codec), codec)
	}

	for _, codec := range []string{"av1", "mpeg4", "wmv3", "vp8", ""} {
		require.Equal(t, domain.DecodeSoftware, caps.DecodePath(domain.EncoderQSV, codec), codec)
	}
}

func TestCapabilities_DecodePathIsSoftwareForTheSoftwareEncoder(t *testing.T) {
	t.Parallel()

	require.Equal(t, domain.DecodeSoftware,
		everythingWorks().DecodePath(domain.EncoderSoftware, "h264"))
}

func TestCapabilities_DecodePathDropsVP9WhenTheDriverDidNotDeliverIt(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Entries: []domain.HWCapability{
		decodeEntry("qsv", false, "no VP9 decoder"),
		decodeEntry("vaapi", true, ""),
	}}

	require.Equal(t, domain.DecodeSoftware, caps.DecodePath(domain.EncoderQSV, "vp9"))
	require.Equal(t, domain.DecodeHardware, caps.DecodePath(domain.EncoderVAAPI, "vp9"))
	require.Equal(t, domain.DecodeHardware, caps.DecodePath(domain.EncoderQSV, "h264"))
}

func TestRetryInSoftware_OnlyOnceAndOnlyFromHardware(t *testing.T) {
	t.Parallel()

	got, ok := hardware.RetryInSoftware(domain.DecodeHardware, false)
	require.True(t, ok)
	require.Equal(t, hardware.DecodeRetry{
		ForceSoftwareDecode: true,
		Reason:              "hardware decode failed; retrying once with software decode plus hwupload",
	}, got)

	_, ok = hardware.RetryInSoftware(domain.DecodeHardware, true)
	require.False(t, ok)

	_, ok = hardware.RetryInSoftware(domain.DecodeSoftware, false)
	require.False(t, ok)
}

func TestCapabilities_RemediationIsEmptyWhenEverythingWorks(t *testing.T) {
	t.Parallel()

	require.Empty(t, everythingWorks().Remediation())
}

func TestCapabilities_RemediationBlamesTheDriverForAMain10OnlyFailure(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Device: device, Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", true, ""),
		encodeEntry("qsv", "main10", false, "Error initializing output stream"),
		encodeEntry("vaapi", "main", true, ""),
		encodeEntry("vaapi", "main10", true, ""),
		decodeEntry("qsv", true, ""),
		decodeEntry("vaapi", true, ""),
	}}

	got := caps.Remediation()
	require.Equal(t, "qsv encodes HEVC Main but not Main10: Error initializing output stream. "+
		"On Gen 9.5 the silicon supports Main10, so this is the driver stack rather than the "+
		"hardware: install intel-media-va-driver-non-free and confirm vainfo lists "+
		"VAProfileHEVCMain10.", got)
}

func TestCapabilities_RemediationNamesTheDeviceWhenABackendIsWhollyAbsent(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Device: device, Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", false, "Cannot load libmfx"),
		encodeEntry("qsv", "main10", false, "Cannot load libmfx"),
		encodeEntry("vaapi", "main", true, ""),
		encodeEntry("vaapi", "main10", true, ""),
		decodeEntry("qsv", true, ""),
		decodeEntry("vaapi", true, ""),
	}}

	require.Equal(t, "qsv does not encode HEVC at all on this host: Cannot load libmfx. "+
		"Check that /dev/dri/renderD128 is present in the container and that the process is "+
		"a member of the render group.", caps.Remediation())
}

func TestCapabilities_RemediationShoutsWhenNothingWorks(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", false, "no device"),
		encodeEntry("qsv", "main10", false, "no device"),
		encodeEntry("vaapi", "main", false, "no device"),
		encodeEntry("vaapi", "main10", false, "no device"),
		decodeEntry("qsv", false, "no device"),
		decodeEntry("vaapi", false, "no device"),
	}}

	got := caps.Remediation()
	require.Contains(t, got, "the render node")
	require.Contains(t, got, "VP9 sources decode in software")
	require.True(t, strings.HasSuffix(got,
		"No hardware encoder works, so every full job runs on libx265. "+
			"Expect several times the wall-clock time per file."), got)
}

func TestCapabilities_RemediationOnAnUnprobedSetAsksForAProbe(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Hardware has not been probed yet. Run the probe from the hardware page.",
		hardware.Capabilities{}.Remediation())
}

func TestCapabilities_RemediationCallsOutABackwardsProfileResult(t *testing.T) {
	t.Parallel()

	caps := hardware.Capabilities{Entries: []domain.HWCapability{
		encodeEntry("qsv", "main", false, "boom"),
		encodeEntry("qsv", "main10", true, ""),
		encodeEntry("vaapi", "main", true, ""),
		encodeEntry("vaapi", "main10", true, ""),
		decodeEntry("qsv", true, ""),
		decodeEntry("vaapi", true, ""),
	}}

	require.Equal(t, "qsv encodes HEVC Main10 but not Main: boom, which is backwards and "+
		"points at the driver stack.", caps.Remediation())
}

func TestProfileFor_TenBitOnlyForHDR(t *testing.T) {
	t.Parallel()

	require.Equal(t, hardware.ProfileMain10, hardware.ProfileFor(true))
	require.Equal(t, hardware.ProfileMain, hardware.ProfileFor(false))
}

func TestBackendOf_RoundTripsTheHardwareEncoders(t *testing.T) {
	t.Parallel()

	for _, b := range hardware.Backends() {
		got, ok := hardware.BackendOf(hardware.EncoderFor(b))
		require.True(t, ok)
		require.Equal(t, b, got)
	}

	_, ok := hardware.BackendOf(domain.EncoderSoftware)
	require.False(t, ok)
}

// The Gen 9.5 set is spelled out in three places: decide, ffmpeg and the list
// the UI renders. This is what catches them drifting apart.
func TestDecodeCodecs_AgreesWithTheDecisionEngine(t *testing.T) {
	t.Parallel()

	for _, codec := range hardware.DecodeCodecs() {
		require.True(t, decide.HardwareDecodable(codec), codec)
		require.True(t, ffmpeg.HardwareDecodable(codec), codec)
	}

	for _, codec := range []string{"av1", "mpeg4", "wmv3", "vp8", "theora", ""} {
		require.False(t, decide.HardwareDecodable(codec), codec)
		require.False(t, ffmpeg.HardwareDecodable(codec), codec)
		require.NotContains(t, hardware.DecodeCodecs(), codec)
	}
}
