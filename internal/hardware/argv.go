package hardware

import "strconv"

// TestSource is the synthetic input every probe encodes: one second at 30 fps exercises
// the whole pipeline and still runs at startup (plan.md 10.1).
const TestSource = "testsrc=size=640x480:rate=30:duration=1"

// vp9SampleBitrate keeps the synthesised clip small; it is decoded, never watched.
const vp9SampleBitrate = 500_000

// VersionArgs asks ffmpeg what build it is, which is the cache key for a probe
// run (plan.md 10.1).
func VersionArgs() []string { return []string{"-hide_banner", "-version"} }

// EncodeArgs is the probe of plan.md 10.1: a real encode, because compiled-in
// support is not working support.
func EncodeArgs(b Backend, p Profile, device string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", string(b) + "=hw:" + device,
		"-filter_hw_device", "hw",
		"-f", "lavfi", "-i", TestSource,
		"-vf", "format=" + uploadPixFmt(p) + "," + hwUpload(b),
		"-c:v", string(EncoderFor(b)), "-profile:v", string(p),
		"-f", "null", "-",
	}
}

// VP9SampleArgs writes the clip the decode probe reads back. lavfi cannot be
// decoded, so the check needs a real VP9 elementary stream first.
func VP9SampleArgs(out string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", TestSource,
		"-c:v", "libvpx-vp9", "-b:v", strconv.Itoa(vp9SampleBitrate),
		"-cpu-used", "8", "-an",
		out,
	}
}

// VP9DecodeArgs decodes the sample on the iGPU, separately from the encode matrix
// because an encode probe says nothing about decode (plan.md 10.1).
func VP9DecodeArgs(b Backend, device, src string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-hwaccel", string(b), "-hwaccel_device", device,
		"-hwaccel_output_format", string(b),
		"-i", src,
		"-f", "null", "-",
	}
}

// uploadPixFmt is the format the frames are converted to before they go to the
// GPU. Testing Main against p010le would prove nothing about 8-bit.
func uploadPixFmt(p Profile) string {
	if p == ProfileMain10 {
		return "p010le"
	}

	return "nv12"
}

// hwUpload is QSV's frame-pool sizing, which VAAPI neither needs nor accepts.
func hwUpload(b Backend) string {
	if b == BackendVAAPI {
		return "hwupload"
	}

	return "hwupload=extra_hw_frames=64"
}
