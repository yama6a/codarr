// Package hardware answers "does this box actually encode HEVC on the iGPU?" with a real
// one-second encode rather than a compiled-in feature list (plan.md 10).
//
// It reports what works; internal/job acts on it.
package hardware

import (
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Backend is a hardware acceleration stack.
type Backend string

// The two backends plan.md 10.2 prefers, in order.
const (
	BackendQSV   Backend = "qsv"
	BackendVAAPI Backend = "vaapi"
)

// Profile is an HEVC encode profile. plan.md 10.1 tests 10-bit separately from
// 8-bit because they fail for different reasons.
type Profile string

// The profiles the encode path can ask for.
const (
	ProfileMain   Profile = "main"
	ProfileMain10 Profile = "main10"
)

// Direction separates an encode probe from a decode probe. The encode probe
// does not cover decode.
type Direction string

// The two probe directions.
const (
	DirectionEncode Direction = "encode"
	DirectionDecode Direction = "decode"
)

// The codecs the probe matrix covers.
const (
	CodecHEVC = "hevc"
	CodecVP9  = "vp9"
)

// Backends is the probe matrix's backend axis, in preference order.
func Backends() []Backend { return []Backend{BackendQSV, BackendVAAPI} }

// Profiles is the probe matrix's profile axis.
func Profiles() []Profile { return []Profile{ProfileMain, ProfileMain10} }

// ProfileFor is the HEVC profile a plan needs. HDR is 10-bit, everything else
// is 8-bit.
func ProfileFor(hdr bool) Profile {
	if hdr {
		return ProfileMain10
	}

	return ProfileMain
}

// DecodeCodecs is the hard-coded Gen 9.5 decode set of plan.md 10.1,
// as the UI lists it. Everything absent from it decodes in software.
func DecodeCodecs() []string {
	return []string{"h264", "hevc", "mpeg2video", "vc1", "vp9"}
}

// EncoderFor is the ffmpeg encoder name for a backend.
func EncoderFor(b Backend) domain.Encoder {
	if b == BackendVAAPI {
		return domain.EncoderVAAPI
	}

	return domain.EncoderQSV
}

// BackendOf is the inverse of EncoderFor. Software has no backend.
func BackendOf(e domain.Encoder) (Backend, bool) {
	switch e {
	case domain.EncoderQSV:
		return BackendQSV, true
	case domain.EncoderVAAPI:
		return BackendVAAPI, true
	case domain.EncoderSoftware:
		return "", false
	default:
		return "", false
	}
}

// Capabilities is one complete probe run: every entry produced by one ffmpeg
// build, plus the device they were probed against.
type Capabilities struct {
	Device        string
	FfmpegVersion string
	ProbedAt      time.Time
	Entries       []domain.HWCapability
}

// Probed reports whether a probe has ever run against this ffmpeg build.
func (c Capabilities) Probed() bool { return len(c.Entries) > 0 }

// Encodes reports whether a real encode of this profile succeeded on this backend;
// an unprobed set answers false throughout, so "not probed yet" falls to software.
func (c Capabilities) Encodes(b Backend, p Profile) bool {
	return c.works(b, DirectionEncode, CodecHEVC, string(p))
}

// DecodesVP9 reports whether the driver delivered VP9 decode. The Gen 9.5 set
// says the silicon has the decoder; this says the stack exposes it.
func (c Capabilities) DecodesVP9(b Backend) bool {
	return c.works(b, DirectionDecode, CodecVP9, "")
}

func (c Capabilities) works(b Backend, d Direction, codec, profile string) bool {
	e, ok := c.entry(b, d, codec, profile)

	return ok && e.Works
}

func (c Capabilities) entry(b Backend, d Direction, codec, profile string) (domain.HWCapability, bool) {
	for _, e := range c.Entries {
		if e.Backend == string(b) && e.Direction == string(d) &&
			e.Codec == codec && e.Profile == profile {
			return e, true
		}
	}

	return domain.HWCapability{}, false
}

// Selection is the encoder one job will use plus why it is not the preferred one,
// because plan.md 10.2 wants a software fallback recorded on the job.
type Selection struct {
	Encoder domain.Encoder
	Profile Profile

	// FellBack is true for anything that is not the preferred QSV.
	FellBack bool

	// Software is the case the UI has to shout about: 20 minutes becomes 4 hours.
	Software bool

	Reason string
}

// Select applies the preference order of plan.md 10.2: QSV, then VAAPI, then
// libx265.
func (c Capabilities) Select(hdr bool) Selection {
	p := ProfileFor(hdr)

	switch {
	case c.Encodes(BackendQSV, p):
		return Selection{Encoder: domain.EncoderQSV, Profile: p}
	case c.Encodes(BackendVAAPI, p):
		return Selection{
			Encoder:  domain.EncoderVAAPI,
			Profile:  p,
			FellBack: true,
			Reason:   "QSV does not encode HEVC " + string(p) + " on this host",
		}
	default:
		return Selection{
			Encoder:  domain.EncoderSoftware,
			Profile:  p,
			FellBack: true,
			Software: true,
			Reason:   c.softwareReason(p),
		}
	}
}

func (c Capabilities) softwareReason(p Profile) string {
	if !c.Probed() {
		return "hardware has not been probed, so no accelerated encoder can be trusted for HEVC " +
			string(p) + "; libx265 is many times slower"
	}

	return "neither QSV nor VAAPI encodes HEVC " + string(p) +
		" on this host; libx265 is many times slower"
}

// Next is the encoder to try after current failed, reporting false at software
// because there is nothing below it.
func (c Capabilities) Next(current domain.Encoder, hdr bool) (Selection, bool) {
	p := ProfileFor(hdr)

	switch current {
	case domain.EncoderQSV:
		if c.Encodes(BackendVAAPI, p) {
			return Selection{
				Encoder:  domain.EncoderVAAPI,
				Profile:  p,
				FellBack: true,
				Reason:   "hevc_qsv failed at runtime",
			}, true
		}

		fallthrough
	case domain.EncoderVAAPI:
		return Selection{
			Encoder:  domain.EncoderSoftware,
			Profile:  p,
			FellBack: true,
			Software: true,
			Reason:   string(current) + " failed at runtime and no other hardware encoder works",
		}, true
	case domain.EncoderSoftware:
		return Selection{}, false
	default:
		return Selection{}, false
	}
}

// DecodePath decides where the video is decoded: only the Gen 9.5 set uses the iGPU, and
// VP9 additionally needs the probe, being in the silicon but not always the driver (10.1).
func (c Capabilities) DecodePath(enc domain.Encoder, sourceCodec string) domain.DecodePath {
	b, ok := BackendOf(enc)
	if !ok {
		return domain.DecodeSoftware
	}

	if !decide.HardwareDecodable(sourceCodec) {
		return domain.DecodeSoftware
	}

	if sourceCodec == CodecVP9 && !c.DecodesVP9(b) {
		return domain.DecodeSoftware
	}

	return domain.DecodeHardware
}

// DecodeRetry is the one software-decode retry of plan.md 10.1; running the job is the
// job package's business.
type DecodeRetry struct {
	ForceSoftwareDecode bool
	Reason              string
}

// RetryInSoftware answers whether a failed job earns the retry: a hardware decode can
// fail on a driver quirk ffprobe read happily, and software has nowhere left to fall.
func RetryInSoftware(decode domain.DecodePath, alreadyRetried bool) (DecodeRetry, bool) {
	if decode != domain.DecodeHardware || alreadyRetried {
		return DecodeRetry{}, false
	}

	return DecodeRetry{
		ForceSoftwareDecode: true,
		Reason:              "hardware decode failed; retrying once with software decode plus hwupload",
	}, true
}

// Remediation is the operator-facing text behind a failed probe. Empty means
// everything works.
func (c Capabilities) Remediation() string {
	if !c.Probed() {
		return "Hardware has not been probed yet. Run the probe from the hardware page."
	}

	var lines []string

	for _, b := range Backends() {
		lines = append(lines, c.backendRemediation(b)...)
	}

	if !c.Encodes(BackendQSV, ProfileMain) && !c.Encodes(BackendVAAPI, ProfileMain) &&
		!c.Encodes(BackendQSV, ProfileMain10) && !c.Encodes(BackendVAAPI, ProfileMain10) {
		lines = append(lines, "No hardware encoder works, so every full job runs on libx265. "+
			"Expect several times the wall-clock time per file.")
	}

	return strings.Join(lines, "\n")
}

func (c Capabilities) backendRemediation(b Backend) []string {
	var lines []string

	main, main10 := c.Encodes(b, ProfileMain), c.Encodes(b, ProfileMain10)

	switch {
	case !main && !main10:
		lines = append(lines, string(b)+" does not encode HEVC at all on this host"+
			c.errSuffix(b, DirectionEncode, CodecHEVC, string(ProfileMain))+
			". Check that "+c.deviceName()+" is present in the container and that the "+
			"process is a member of the render group.")
	case main && !main10:
		// plan.md 10.1: Gen 9.5 silicon does Main10. A Main10-only failure is
		// the driver stack, so say so rather than sending someone shopping.
		lines = append(lines, string(b)+" encodes HEVC Main but not Main10"+
			c.errSuffix(b, DirectionEncode, CodecHEVC, string(ProfileMain10))+
			". On Gen 9.5 the silicon supports Main10, so this is the driver stack rather than "+
			"the hardware: install intel-media-va-driver-non-free and confirm vainfo lists "+
			"VAProfileHEVCMain10.")
	case !main && main10:
		lines = append(lines, string(b)+" encodes HEVC Main10 but not Main"+
			c.errSuffix(b, DirectionEncode, CodecHEVC, string(ProfileMain))+
			", which is backwards and points at the driver stack.")
	}

	if !c.DecodesVP9(b) {
		lines = append(lines, string(b)+" did not decode VP9"+
			c.errSuffix(b, DirectionDecode, CodecVP9, "")+
			". VP9 sources will decode in software, which costs CPU but is otherwise correct.")
	}

	return lines
}

func (c Capabilities) errSuffix(b Backend, d Direction, codec, profile string) string {
	e, ok := c.entry(b, d, codec, profile)
	if !ok || e.Error == "" {
		return ""
	}

	return ": " + e.Error
}

func (c Capabilities) deviceName() string {
	if c.Device == "" {
		return "the render node"
	}

	return c.Device
}
