// Package domain holds the types shared across every Codarr package. Nothing in
// here talks to a database, a filesystem or a subprocess.
package domain

import (
	"path/filepath"
	"strings"
)

// Decision is what happens to one stream of one file.
type Decision string

const (
	DecisionCopy    Decision = "copy"
	DecisionEncode  Decision = "encode"
	DecisionConvert Decision = "convert"
	DecisionDrop    Decision = "drop"
)

// Kind is the overall shape of the work a file needs.
type Kind string

const (
	KindSkip      Kind = "skip"
	KindRemux     Kind = "remux"
	KindAudioOnly Kind = "audio_only"
	KindFull      Kind = "full"
)

// StreamType is the ffprobe codec_type of a stream Codarr cares about.
type StreamType string

const (
	StreamVideo    StreamType = "video"
	StreamAudio    StreamType = "audio"
	StreamSubtitle StreamType = "subtitle"
)

// Container is the output muxer family. Codarr only ever writes these two.
type Container string

const (
	ContainerMatroska Container = "matroska"
	ContainerMP4      Container = "mp4"
)

// OutputExt is the extension the output must carry for a given source path.
//
// It takes the source path rather than returning a constant because plan.md 6.1
// requires the filename never to change for an MKV or MP4 source: an .m4v file
// stays .m4v, and returning ".mp4" for it would rename the file, which makes an
// *arr rescan a delete-plus-add instead of a no-op. Only a legacy container,
// which is becoming MKV anyway, gets a new extension.
func (c Container) OutputExt(sourcePath string) string {
	srcExt := filepath.Ext(sourcePath)
	lower := strings.ToLower(srcExt)

	switch c {
	case ContainerMP4:
		if lower == ".m4v" || lower == ".mp4" {
			return srcExt
		}

		return ".mp4"
	case ContainerMatroska:
		if lower == ".mkv" {
			return srcExt
		}

		return ".mkv"
	default:
		return ".mkv"
	}
}

// DecodePath records whether the video was decoded on the iGPU or in software.
type DecodePath string

const (
	DecodeHardware DecodePath = "hardware"
	DecodeSoftware DecodePath = "software"
)

// Encoder is the video encoder actually used for a job.
type Encoder string

const (
	EncoderQSV      Encoder = "hevc_qsv"
	EncoderVAAPI    Encoder = "hevc_vaapi"
	EncoderSoftware Encoder = "libx265"
)

// Scan is the interlacing state of a video stream. Unknown counts as
// progressive: ffprobe omits field_order for a large share of progressive
// files, and treating that as interlaced full-encodes much of a normal library.
type Scan string

const (
	ScanProgressive Scan = "progressive"
	ScanInterlaced  Scan = "interlaced"
)
