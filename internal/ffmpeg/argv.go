// Package ffmpeg builds ffmpeg argument vectors, runs them, and parses the
// progress stream they emit.
package ffmpeg

import (
	"errors"
	"strconv"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

var (
	ErrNothingToDo    = errors.New("ffmpeg: plan produces no output file")
	ErrNoVideoStream  = errors.New("ffmpeg: plan maps no video stream")
	ErrNoAudioStream  = errors.New("ffmpeg: plan maps no audio stream")
	ErrMissingCodec   = errors.New("ffmpeg: stream has no target codec")
	ErrMissingBitrate = errors.New("ffmpeg: video encode has no target bitrate")
	ErrMissingEncoder = errors.New("ffmpeg: video encode has no encoder")
	ErrMissingDevice  = errors.New("ffmpeg: hardware encode has no render device")
)

const (
	hwUploadFilter   = "hwupload=extra_hw_frames=64"
	swDeinterlace    = "bwdif=mode=send_frame"
	qsvDeinterlace   = "vpp_qsv=deinterlace=2"
	vaapiDeinterlace = "deinterlace_vaapi"
	levelRewriteBSF  = "h264_metadata=level=4.2"
)

// Source is the probe-derived facts about the input that argv construction
// depends on. internal/ffprobe supplies it.
type Source struct {
	Path string

	// VideoCodec is the ffprobe codec_name of the primary video stream. It
	// selects the decode path (10.1) and decides hvc1 tagging on a copy (6.2).
	VideoCodec string

	// LegacyContainer marks AVI, VOB, MPG, TS, FLV and friends, whose
	// timestamps die with non-monotonic DTS without -fflags +genpts (14.1).
	LegacyContainer bool
}

// Tags are the loop-prevention markers written into every output (12).
type Tags struct {
	Version string
	Policy  string
}

// Request is everything Build needs on top of the plan.
type Request struct {
	Plan   domain.Plan
	Source Source
	Output string
	Tags   Tags

	// Encoder and Device are only read when the plan encodes video.
	Encoder domain.Encoder
	Device  string

	// ForceSoftwareDecode is the retry after a hardware decode failed at
	// runtime (10.1).
	ForceSoftwareDecode bool
}

// Command is a built invocation. The binary is not part of it; the runner
// prepends the injected path (21).
type Command struct {
	Args       []string
	DecodePath domain.DecodePath
}

// HardwareDecodable reports whether Gen 9.5 has a fixed-function decoder for a
// codec. Passing -hwaccel for anything else fails on exactly the sources the
// encode path exists for (10.1).
func HardwareDecodable(codec string) bool {
	switch codec {
	case "h264", "hevc", "mpeg2video", "vc1", "vp9":
		return true
	default:
		return false
	}
}

// outStream is one kept stream at its position in the output file. The output
// position is derived here and nowhere else, because -c:a:N, -b:a:N,
// -metadata:s:a:N, -disposition:a:N and -bsf:v:N all address it rather than the
// source position (14.2).
type outStream struct {
	plan   domain.StreamPlan
	letter string
	spec   string
}

// Build turns a plan into the exact argv for it.
func Build(req Request) (Command, error) {
	if !req.Plan.NeedsWrite() {
		return Command{}, ErrNothingToDo
	}

	outs := outputStreams(req.Plan)
	if err := validate(req, outs); err != nil {
		return Command{}, err
	}

	decode := decodePath(req)

	args := make([]string, 0, 64)
	args = append(args, "-hide_banner", "-nostdin", "-y")
	args = append(args, hwDeviceArgs(req)...)
	args = append(args, hwAccelArgs(req, decode)...)

	if req.Source.LegacyContainer {
		args = append(args, "-fflags", "+genpts")
	}

	args = append(args, "-i", req.Source.Path)

	for _, o := range outs {
		args = append(args, streamArgs(req, o, decode)...)
	}

	args = append(args, "-map_metadata", "0", "-map_chapters", "0")

	if req.Plan.OutputContainer == domain.ContainerMP4 {
		args = append(args, "-movflags", strings.Join(MP4Movflags(), ""))
	}

	args = append(args,
		"-metadata", "CODARR=1",
		"-metadata", "CODARR_VERSION="+req.Tags.Version,
		"-metadata", "CODARR_POLICY="+req.Tags.Policy,
		"-progress", "pipe:1",
		"-nostats",
		req.Output,
	)

	return Command{Args: args, DecodePath: decode}, nil
}

// outputStreams builds the map list first, in output order, so every indexed
// option can be assigned by enumerating it (14.2).
func outputStreams(p domain.Plan) []outStream {
	outs := make([]outStream, 0, len(p.Streams))

	groups := []struct {
		typ    domain.StreamType
		letter string
	}{
		{domain.StreamVideo, "v"},
		{domain.StreamAudio, "a"},
		{domain.StreamSubtitle, "s"},
	}

	for _, g := range groups {
		n := 0

		for _, s := range p.Streams {
			if s.Type != g.typ || s.Decision == domain.DecisionDrop {
				continue
			}

			outs = append(outs, outStream{plan: s, letter: g.letter, spec: g.letter + ":" + strconv.Itoa(n)})
			n++
		}
	}

	return outs
}

func validate(req Request, outs []outStream) error {
	video, audio := 0, 0

	for _, o := range outs {
		switch o.plan.Type {
		case domain.StreamVideo:
			video++
		case domain.StreamAudio:
			audio++
		case domain.StreamSubtitle:
		}

		if o.plan.Decision != domain.DecisionCopy && o.plan.TargetCodec == "" {
			return ErrMissingCodec
		}
	}

	if video == 0 {
		return ErrNoVideoStream
	}

	if audio == 0 {
		return ErrNoAudioStream
	}

	return validateEncode(req)
}

func validateEncode(req Request) error {
	if !encodesVideo(req.Plan) {
		return nil
	}

	switch {
	case req.Encoder == "":
		return ErrMissingEncoder
	case req.Plan.TargetVideoBitrate <= 0:
		return ErrMissingBitrate
	case req.Encoder != domain.EncoderSoftware && req.Device == "":
		return ErrMissingDevice
	default:
		return nil
	}
}

func encodesVideo(p domain.Plan) bool {
	v, ok := p.VideoStream()

	return ok && v.Decision == domain.DecisionEncode
}

// decodePath picks hardware decode only when the source codec has a
// fixed-function decoder and the frames it produces stay on the GPU (14.1).
func decodePath(req Request) domain.DecodePath {
	switch {
	case !encodesVideo(req.Plan):
		return domain.DecodeSoftware
	case req.ForceSoftwareDecode:
		return domain.DecodeSoftware
	case req.Encoder == domain.EncoderSoftware:
		return domain.DecodeSoftware
	case !HardwareDecodable(req.Source.VideoCodec):
		return domain.DecodeSoftware
	default:
		return domain.DecodeHardware
	}
}

func hwDeviceArgs(req Request) []string {
	if !encodesVideo(req.Plan) || req.Encoder == domain.EncoderSoftware {
		return nil
	}

	return []string{"-init_hw_device", backend(req.Encoder) + "=hw:" + req.Device, "-filter_hw_device", "hw"}
}

func hwAccelArgs(req Request, decode domain.DecodePath) []string {
	if decode != domain.DecodeHardware {
		return nil
	}

	b := backend(req.Encoder)

	return []string{"-hwaccel", b, "-hwaccel_output_format", b}
}

func backend(e domain.Encoder) string {
	if e == domain.EncoderVAAPI {
		return "vaapi"
	}

	return "qsv"
}

func streamArgs(req Request, o outStream, decode domain.DecodePath) []string {
	args := []string{"-map", "0:" + o.letter + ":" + strconv.Itoa(o.plan.SourceIndex)}

	switch o.plan.Type {
	case domain.StreamVideo:
		args = append(args, videoArgs(req, o, decode)...)
	case domain.StreamAudio:
		args = append(args, audioArgs(o)...)
	case domain.StreamSubtitle:
		args = append(args, "-c:"+o.spec, subtitleCodec(o))
	}

	args = append(args, metadataArgs(o)...)

	return append(args, "-disposition:"+o.spec, dispositionValue(o.plan))
}

func videoArgs(req Request, o outStream, decode domain.DecodePath) []string {
	if o.plan.Decision != domain.DecisionEncode {
		return copiedVideoArgs(req)
	}

	args := make([]string, 0, 16)

	if chain := videoFilters(req, decode); chain != "" {
		args = append(args, "-vf", chain)
	}

	args = append(args, "-c:v", string(req.Encoder), "-profile:v", videoProfile(req.Plan))

	// -pix_fmt only ever appears on the pure software encode; on a hardware
	// pipeline it conflicts with QSV surfaces, so the format goes in the filter
	// chain instead (14.1).
	if req.Encoder == domain.EncoderSoftware {
		args = append(args, "-pix_fmt", softwarePixFmt(req.Plan))
	}

	if req.Plan.HDR {
		args = append(args, "-color_primaries", "bt2020", "-color_trc", hdrTransfer(req.Plan), "-colorspace", "bt2020nc")
	}

	if req.Plan.OutputContainer == domain.ContainerMP4 {
		args = append(args, "-tag:v", "hvc1")
	}

	return append(args, rateControlArgs(req.Plan.TargetVideoBitrate)...)
}

func copiedVideoArgs(req Request) []string {
	args := []string{"-c:v", "copy"}

	if req.Plan.OutputContainer == domain.ContainerMP4 && req.Source.VideoCodec == "hevc" {
		args = append(args, "-tag:v", "hvc1")
	}

	// 6.2: a level-only failure that fits 4.2 is a flag rewrite in the copied
	// stream, not a re-encode.
	if req.Plan.LevelRewrite {
		args = append(args, "-bsf:v", levelRewriteBSF)
	}

	return args
}

func videoFilters(req Request, decode domain.DecodePath) string {
	var chain []string

	if decode == domain.DecodeHardware {
		if req.Plan.Deinterlace {
			chain = append(chain, hwDeinterlace(req.Encoder))
		}

		return strings.Join(chain, ",")
	}

	// bwdif is a software filter and cannot run on QSV frames, so on the
	// software-decode path it goes before the upload (9, 14.1).
	if req.Plan.Deinterlace {
		chain = append(chain, swDeinterlace)
	}

	if req.Encoder != domain.EncoderSoftware {
		chain = append(chain, "format="+uploadPixFmt(req.Plan), hwUploadFilter)
	}

	return strings.Join(chain, ",")
}

func hwDeinterlace(e domain.Encoder) string {
	if e == domain.EncoderVAAPI {
		return vaapiDeinterlace
	}

	return qsvDeinterlace
}

func videoProfile(p domain.Plan) string {
	if p.HDR {
		return "main10"
	}

	return "main"
}

func uploadPixFmt(p domain.Plan) string {
	if p.HDR {
		return "p010le"
	}

	return "nv12"
}

func softwarePixFmt(p domain.Plan) string {
	if p.HDR {
		return "yuv420p10le"
	}

	return "yuv420p"
}

// rateControlArgs is the 8.3 rate control triple. The multipliers are exact
// integer ratios so the argv is reproducible.
func rateControlArgs(target int) []string {
	return []string{
		"-b:v", strconv.Itoa(target),
		"-maxrate", strconv.Itoa(target * maxrateNum / maxrateDen),
		"-bufsize", strconv.Itoa(target * bufsizeNum / bufsizeDen),
	}
}

func audioArgs(o outStream) []string {
	args := []string{"-c:" + o.spec, audioCodec(o)}

	if o.plan.Decision == domain.DecisionCopy {
		return args
	}

	if o.plan.TargetBitrate > 0 {
		args = append(args, "-b:"+o.spec, bitrateArg(o.plan.TargetBitrate))
	}

	if o.plan.TargetChannels > 0 {
		args = append(args, "-ac:"+o.spec, strconv.Itoa(o.plan.TargetChannels))
	}

	return args
}

func audioCodec(o outStream) string {
	if o.plan.Decision == domain.DecisionCopy {
		return "copy"
	}

	return o.plan.TargetCodec
}

func subtitleCodec(o outStream) string {
	if o.plan.Decision == domain.DecisionCopy {
		return "copy"
	}

	return o.plan.TargetCodec
}

func bitrateArg(bps int) string {
	if bps%1000 == 0 {
		return strconv.Itoa(bps/1000) + "k"
	}

	return strconv.Itoa(bps)
}

func metadataArgs(o outStream) []string {
	var args []string

	if o.plan.Language != "" {
		args = append(args, "-metadata:s:"+o.spec, "language="+o.plan.Language)
	}

	if o.plan.Title != "" {
		args = append(args, "-metadata:s:"+o.spec, "title="+o.plan.Title)
	}

	return args
}

// dispositionValue is always emitted: ffmpeg does not reliably carry
// dispositions through a container rebuild (6.3).
func dispositionValue(s domain.StreamPlan) string {
	var flags []string

	if s.Default {
		flags = append(flags, "default")
	}

	if s.Forced {
		flags = append(flags, "forced")
	}

	if s.Comment {
		flags = append(flags, "comment")
	}

	if s.VisualImpaired {
		flags = append(flags, "visual_impaired")
	}

	if len(flags) == 0 {
		return "0"
	}

	return strings.Join(flags, "+")
}

// hdrTransfer returns the transfer characteristic to stamp on an HDR encode.
// plan.md 9 hard-codes smpte2084, which is wrong for the HLG half of its own HDR
// test: an arib-std-b67 source re-encoded as PQ renders washed out.
func hdrTransfer(p domain.Plan) string {
	if p.HDRTransfer == "arib-std-b67" {
		return "arib-std-b67"
	}

	return "smpte2084"
}
