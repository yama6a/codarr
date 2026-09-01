package hardware_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/hardware"
)

const device = "/dev/dri/renderD128"

func TestEncodeArgs_MatchesThePlansProbeCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", "qsv=hw:/dev/dri/renderD128",
		"-filter_hw_device", "hw",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=1",
		"-vf", "format=p010le,hwupload=extra_hw_frames=64",
		"-c:v", "hevc_qsv", "-profile:v", "main10",
		"-f", "null", "-",
	}, hardware.EncodeArgs(hardware.BackendQSV, hardware.ProfileMain10, device))
}

func TestEncodeArgs_EightBitUploadsNV12(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", "qsv=hw:/dev/dri/renderD128",
		"-filter_hw_device", "hw",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=1",
		"-vf", "format=nv12,hwupload=extra_hw_frames=64",
		"-c:v", "hevc_qsv", "-profile:v", "main",
		"-f", "null", "-",
	}, hardware.EncodeArgs(hardware.BackendQSV, hardware.ProfileMain, device))
}

func TestEncodeArgs_VAAPIUsesItsOwnEncoderAndPlainHwupload(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", "vaapi=hw:/dev/dri/renderD128",
		"-filter_hw_device", "hw",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=1",
		"-vf", "format=p010le,hwupload",
		"-c:v", "hevc_vaapi", "-profile:v", "main10",
		"-f", "null", "-",
	}, hardware.EncodeArgs(hardware.BackendVAAPI, hardware.ProfileMain10, device))
}

func TestVP9SampleArgs_WritesARealFile(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=1",
		"-c:v", "libvpx-vp9", "-b:v", "500000",
		"-cpu-used", "8", "-an",
		"/tmp/probe.webm",
	}, hardware.VP9SampleArgs("/tmp/probe.webm"))
}

func TestVP9DecodeArgs_KeepsTheFramesOnTheGPU(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-hwaccel", "qsv", "-hwaccel_device", "/dev/dri/renderD128",
		"-hwaccel_output_format", "qsv",
		"-i", "/tmp/probe.webm",
		"-f", "null", "-",
	}, hardware.VP9DecodeArgs(hardware.BackendQSV, device, "/tmp/probe.webm"))
}

func TestVersionArgs_IsTheCacheKeyQuery(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"-hide_banner", "-version"}, hardware.VersionArgs())
}
