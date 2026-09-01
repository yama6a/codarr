// Package decide turns an ffprobe result into a plan. The policy it applies is
// hard-coded (plan.md 3, rule 3): nothing here is configurable, and the hash of
// these constants is what makes an already-processed file identifiable.
package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Video copy test, plan.md 6.2. The profile strings are ffprobe's exact
// spelling; "Constrained Baseline" is a distinct value from "Baseline".
var (
	videoCopyCodecs  = []string{"h264", "hevc"}
	h264CopyProfiles = []string{"Constrained Baseline", "Baseline", "Main", "High"}
	hevcCopyProfiles = []string{"Main", "Main 10"}
	copyChroma       = ffprobe.Chroma420
)

// h264MaxLevel is the level ceiling, applied to h264 only. HEVC has no
// equivalent problem at 4K because 4K hardware decoders were built around it.
const h264MaxLevel = 4.2

// Level rewrite carve-out, plan.md 6.2. maxRefs is load-bearing rather than a
// margin: level 4.2 allows MaxDpbMbs 34816 and a 1080p frame is 8160
// macroblocks, so the DPB holds exactly 4 reference frames.
const (
	levelRewriteTarget    = 4.2
	levelRewriteMaxWidth  = 1920
	levelRewriteMaxHeight = 1088
	levelRewriteMaxFPS    = 60
	levelRewriteMaxRefs   = 4
)

// Video encode target, plan.md 6.2. One codec family, all resolutions.
const (
	videoEncodeCodec      = "hevc"
	videoEncodeProfileSDR = "main"
	videoEncodeProfileHDR = "main10"
)

// legacyScanCodecs are the encode-path codecs where an absent field_order is
// worth an idet sample: baked-in combing actually happens on these.
var legacyScanCodecs = []string{"mpeg2video", "vc1"}

// Audio copy lists, plan.md 6.3. MP3 is stereo-and-below only.
var (
	audioCopyUpTo2Channels = []string{"aac", "ac3", "eac3", "mp3"}
	audioCopy3PlusChannels = []string{"aac", "ac3", "eac3"}
)

// Audio encode targets, plan.md 6.3. AC3 caps at 5.1, which is why 7.1 and
// above downmix - and only ever when a conversion was required anyway.
const (
	audioMonoCodec       = "aac"
	audioMonoBitrate     = 96_000
	audioStereoCodec     = "aac"
	audioStereoBitrate   = 160_000
	audioSurroundCodec   = "ac3"
	audioSurroundBitrate = 640_000

	// AC3 in MP4 is legal but patchily supported, so MP4 gets multichannel AAC.
	audioMP4SurroundCodec      = "aac"
	audioMP4BitratePerChannel  = 64_000
	audioDownmixAboveChannels  = 6
	audioDownmixTargetChannels = 6
)

// Subtitle policy, plan.md 6.4. Image and broadcast formats drop even when
// forced; every standalone text format converts and is never dropped.
var (
	subtitleTextCodecs      = []string{"subrip", "ass", "ssa", "webvtt", "mov_text"}
	subtitleImageCodecs     = []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle"}
	subtitleBroadcastCodecs = []string{"dvb_teletext", "eia_608"}
)

const (
	subtitleTargetMKV = "subrip"
	subtitleTargetMP4 = "mov_text"
)

// Container preservation, plan.md 6.1. Anything not listed becomes MKV, which
// is the only case where Codarr changes a filename.
var containerByExt = map[string]domain.Container{
	".mkv": domain.ContainerMatroska,
	".mp4": domain.ContainerMP4,
	".m4v": domain.ContainerMP4,
}

const containerFallback = domain.ContainerMatroska

// hardwareDecodeCodecs is the Gen 9.5 decode set from plan.md 10.1. It is not
// part of the policy hash: it selects a decode path, not what the output
// contains.
var hardwareDecodeCodecs = []string{"h264", "hevc", "mpeg2video", "vc1", "vp9"}

// HardwareDecodable reports whether the iGPU can decode a source codec. Passing
// -hwaccel for anything else fails on exactly the sources the encode path
// exists for.
func HardwareDecodable(codec string) bool {
	return slices.Contains(hardwareDecodeCodecs, codec)
}

// ContainerFor is the output container for a source path, plan.md 6.1.
func ContainerFor(path string) domain.Container {
	if c, ok := containerByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return c
	}

	return containerFallback
}

// IsLegacyContainer reports a source container that is neither Matroska nor
// MP4. Those get -fflags +genpts on input (plan.md 14.1) and always change
// extension.
func IsLegacyContainer(sourceContainer string) bool {
	return sourceContainer != string(domain.ContainerMatroska) && sourceContainer != string(domain.ContainerMP4)
}

// SubtitleTargetForContainer is the text subtitle codec an output container
// takes, as ffprobe names it.
func SubtitleTargetForContainer(c domain.Container) string {
	if c == domain.ContainerMP4 {
		return subtitleTargetMP4
	}

	return subtitleTargetMKV
}

// SubtitleEncoder maps a target subtitle codec to the ffmpeg encoder name.
// ffmpeg writes subrip with the "srt" encoder, so the two differ.
func SubtitleEncoder(codec string) string {
	if codec == subtitleTargetMKV {
		return "srt"
	}

	return codec
}

// VideoEncodeProfile is the HEVC profile for an encode: Main 10 for HDR
// sources, which are never tonemapped, Main otherwise.
func VideoEncodeProfile(hdr bool) string {
	if hdr {
		return videoEncodeProfileHDR
	}

	return videoEncodeProfileSDR
}

// audioTarget is what one audio stream is converted to.
type audioTarget struct {
	Codec    string
	Bitrate  int
	Channels int
}

func audioEncodeTarget(container domain.Container, channels int) audioTarget {
	switch {
	case channels <= 1:
		return audioTarget{Codec: audioMonoCodec, Bitrate: audioMonoBitrate, Channels: 1}
	case channels == 2:
		return audioTarget{Codec: audioStereoCodec, Bitrate: audioStereoBitrate, Channels: 2}
	}

	out := channels
	if out > audioDownmixAboveChannels {
		out = audioDownmixTargetChannels
	}

	if container == domain.ContainerMP4 {
		return audioTarget{Codec: audioMP4SurroundCodec, Bitrate: audioMP4BitratePerChannel * out, Channels: out}
	}

	return audioTarget{Codec: audioSurroundCodec, Bitrate: audioSurroundBitrate, Channels: out}
}

func audioCopyList(channels int) []string {
	if channels <= 2 {
		return audioCopyUpTo2Channels
	}

	return audioCopy3PlusChannels
}

// policySnapshot is every constant the hash covers, in one value, so the hash
// is computed from the same place the engine reads.
type policySnapshot struct {
	VideoCopyCodecs  []string
	H264CopyProfiles []string
	HevcCopyProfiles []string
	CopyChroma       string
	H264MaxLevel     float64

	LevelRewriteTarget    float64
	LevelRewriteMaxWidth  int
	LevelRewriteMaxHeight int
	LevelRewriteMaxFPS    float64
	LevelRewriteMaxRefs   int

	VideoEncodeCodec      string
	VideoEncodeProfileSDR string
	VideoEncodeProfileHDR string
	LegacyScanCodecs      []string

	AudioCopyUpTo2Channels []string
	AudioCopy3PlusChannels []string
	AudioTargets           []audioTarget

	SubtitleTextCodecs      []string
	SubtitleImageCodecs     []string
	SubtitleBroadcastCodecs []string
	SubtitleTargetMKV       string
	SubtitleTargetMP4       string

	ContainerByExt   map[string]domain.Container
	ContainerDefault domain.Container
}

func currentPolicy() policySnapshot {
	var targets []audioTarget

	for _, c := range []domain.Container{domain.ContainerMatroska, domain.ContainerMP4} {
		for _, ch := range []int{1, 2, 3, 6, 8} {
			targets = append(targets, audioEncodeTarget(c, ch))
		}
	}

	return policySnapshot{
		VideoCopyCodecs:  videoCopyCodecs,
		H264CopyProfiles: h264CopyProfiles,
		HevcCopyProfiles: hevcCopyProfiles,
		CopyChroma:       string(copyChroma),
		H264MaxLevel:     h264MaxLevel,

		LevelRewriteTarget:    levelRewriteTarget,
		LevelRewriteMaxWidth:  levelRewriteMaxWidth,
		LevelRewriteMaxHeight: levelRewriteMaxHeight,
		LevelRewriteMaxFPS:    levelRewriteMaxFPS,
		LevelRewriteMaxRefs:   levelRewriteMaxRefs,

		VideoEncodeCodec:      videoEncodeCodec,
		VideoEncodeProfileSDR: videoEncodeProfileSDR,
		VideoEncodeProfileHDR: videoEncodeProfileHDR,
		LegacyScanCodecs:      legacyScanCodecs,

		AudioCopyUpTo2Channels: audioCopyUpTo2Channels,
		AudioCopy3PlusChannels: audioCopy3PlusChannels,
		AudioTargets:           targets,

		SubtitleTextCodecs:      subtitleTextCodecs,
		SubtitleImageCodecs:     subtitleImageCodecs,
		SubtitleBroadcastCodecs: subtitleBroadcastCodecs,
		SubtitleTargetMKV:       subtitleTargetMKV,
		SubtitleTargetMP4:       subtitleTargetMP4,

		ContainerByExt:   containerByExt,
		ContainerDefault: containerFallback,
	}
}

// policyHashLength keeps the tag short enough to read in a log line. It is a
// prefix of a SHA-256, so collisions are not a concern at this scale.
const policyHashLength = 8

var policyHash = sync.OnceValue(func() string { return hashPolicy(currentPolicy()) })

// PolicyHash identifies the policy that produced a file. Changing any constant
// above changes it, which is what makes previously-tagged files eligible again
// after a rebuild (plan.md 12).
func PolicyHash() string { return policyHash() }

func hashPolicy(p policySnapshot) string {
	var b strings.Builder

	writeSet := func(name string, vs []string) {
		sorted := slices.Clone(vs)
		slices.Sort(sorted)
		fmt.Fprintf(&b, "%s=%s\n", name, strings.Join(sorted, "|"))
	}

	writeSet("video.codecs", p.VideoCopyCodecs)
	writeSet("video.h264.profiles", p.H264CopyProfiles)
	writeSet("video.hevc.profiles", p.HevcCopyProfiles)
	fmt.Fprintf(&b, "video.chroma=%s\n", p.CopyChroma)
	fmt.Fprintf(&b, "video.h264.maxlevel=%g\n", p.H264MaxLevel)
	fmt.Fprintf(&b, "video.levelrewrite=%g/%d/%d/%g/%d\n",
		p.LevelRewriteTarget, p.LevelRewriteMaxWidth, p.LevelRewriteMaxHeight,
		p.LevelRewriteMaxFPS, p.LevelRewriteMaxRefs)
	fmt.Fprintf(&b, "video.encode=%s/%s/%s\n", p.VideoEncodeCodec, p.VideoEncodeProfileSDR, p.VideoEncodeProfileHDR)
	writeSet("video.legacyscan", p.LegacyScanCodecs)

	writeSet("audio.copy.upto2", p.AudioCopyUpTo2Channels)
	writeSet("audio.copy.3plus", p.AudioCopy3PlusChannels)

	for _, t := range p.AudioTargets {
		fmt.Fprintf(&b, "audio.target=%s/%d/%d\n", t.Codec, t.Bitrate, t.Channels)
	}

	writeSet("subtitle.text", p.SubtitleTextCodecs)
	writeSet("subtitle.image", p.SubtitleImageCodecs)
	writeSet("subtitle.broadcast", p.SubtitleBroadcastCodecs)
	fmt.Fprintf(&b, "subtitle.target=%s/%s\n", p.SubtitleTargetMKV, p.SubtitleTargetMP4)

	exts := make([]string, 0, len(p.ContainerByExt))
	for ext := range p.ContainerByExt {
		exts = append(exts, ext)
	}

	slices.Sort(exts)

	for _, ext := range exts {
		fmt.Fprintf(&b, "container=%s/%s\n", ext, p.ContainerByExt[ext])
	}

	fmt.Fprintf(&b, "container.default=%s\n", p.ContainerDefault)

	sum := sha256.Sum256([]byte(b.String()))

	return hex.EncodeToString(sum[:])[:policyHashLength]
}
