package hardware_test

import (
	"slices"
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

func TestSampleArgs_WritesARealFilePerEncoder(t *testing.T) {
	t.Parallel()

	head := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=1",
	}

	require.Equal(t, append(slices.Clone(head),
		"-c:v", "libvpx-vp9", "-b:v", "500000", "-cpu-used", "8", "-an", "/tmp/probe.webm"),
		hardware.SampleArgs("libvpx-vp9", "/tmp/probe.webm"))
	require.Equal(t, append(slices.Clone(head),
		"-c:v", "libsvtav1", "-b:v", "500000", "-preset", "12", "-an", "/tmp/probe.mkv"),
		hardware.SampleArgs("libsvtav1", "/tmp/probe.mkv"))
	require.Equal(t, append(slices.Clone(head),
		"-c:v", "libaom-av1", "-b:v", "500000", "-cpu-used", "8", "-usage", "realtime", "-an", "/tmp/probe.mkv"),
		hardware.SampleArgs("libaom-av1", "/tmp/probe.mkv"))
}

func TestDecodeProbes_CoverVP9AndAV1(t *testing.T) {
	t.Parallel()

	require.Equal(t, []hardware.DecodeProbe{
		{Codec: "vp9", Encoders: []string{"libvpx-vp9"}, Ext: ".webm"},
		{Codec: "av1", Encoders: []string{"libsvtav1", "libaom-av1"}, Ext: ".mkv"},
	}, hardware.DecodeProbes())
}

func TestDecodeArgs_KeepsTheFramesOnTheGPU(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-hwaccel", "qsv", "-hwaccel_device", "/dev/dri/renderD128",
		"-hwaccel_output_format", "qsv",
		"-i", "/tmp/probe.webm",
		"-f", "null", "-",
	}, hardware.DecodeArgs(hardware.BackendQSV, device, "/tmp/probe.webm"))
}

func TestVersionArgs_IsTheCacheKeyQuery(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"-hide_banner", "-version"}, hardware.VersionArgs())
}
