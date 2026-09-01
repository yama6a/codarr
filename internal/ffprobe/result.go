package ffprobe

import (
	"math"
	"strconv"
	"strings"
)

// Result is one ffprobe run.
type Result struct {
	Format   Format    `json:"format"`
	Streams  []Stream  `json:"streams"`
	Chapters []Chapter `json:"chapters"`

	// Raw is what ffprobe printed, so media_files.probe_json stores the source
	// of truth rather than a re-marshalling of this struct.
	Raw []byte `json:"-"`
}

// Format is the container.
type Format struct {
	Filename       string            `json:"filename"`
	NBStreams      int               `json:"nb_streams"`
	FormatName     string            `json:"format_name"`
	FormatLongName string            `json:"format_long_name"`
	StartTime      string            `json:"start_time"`
	Duration       string            `json:"duration"`
	Size           string            `json:"size"`
	BitRate        string            `json:"bit_rate"`
	ProbeScore     int               `json:"probe_score"`
	Tags           map[string]string `json:"tags"`
}

// Stream is one elementary stream.
type Stream struct {
	Index          int               `json:"index"`
	CodecName      string            `json:"codec_name"`
	CodecLongName  string            `json:"codec_long_name"`
	CodecType      string            `json:"codec_type"`
	CodecTagString string            `json:"codec_tag_string"`
	Profile        string            `json:"profile"`
	Level          int               `json:"level"`
	Width          int               `json:"width"`
	Height         int               `json:"height"`
	CodedWidth     int               `json:"coded_width"`
	CodedHeight    int               `json:"coded_height"`
	PixFmt         string            `json:"pix_fmt"`
	RFrameRate     string            `json:"r_frame_rate"`
	AvgFrameRate   string            `json:"avg_frame_rate"`
	TimeBase       string            `json:"time_base"`
	Duration       string            `json:"duration"`
	Channels       int               `json:"channels"`
	ChannelLayout  string            `json:"channel_layout"`
	SampleRate     string            `json:"sample_rate"`
	SampleFmt      string            `json:"sample_fmt"`
	BitRate        string            `json:"bit_rate"`
	FieldOrder     string            `json:"field_order"`
	ColorTransfer  string            `json:"color_transfer"`
	ColorPrimaries string            `json:"color_primaries"`
	ColorSpace     string            `json:"color_space"`
	ColorRange     string            `json:"color_range"`
	Refs           int               `json:"refs"`
	Disposition    Disposition       `json:"disposition"`
	Tags           map[string]string `json:"tags"`
	SideDataList   []SideData        `json:"side_data_list"`
}

// Disposition is the subset of ffprobe's disposition flags Codarr acts on.
type Disposition struct {
	Default        int `json:"default"`
	Forced         int `json:"forced"`
	Comment        int `json:"comment"`
	HearingImpair  int `json:"hearing_impaired"`
	VisualImpaired int `json:"visual_impaired"`
	AttachedPic    int `json:"attached_pic"`
}

// SideData is one entry of a stream's side_data_list. The Dolby Vision
// configuration record arrives here; see plan.md 9.
type SideData struct {
	Type string `json:"side_data_type"`

	DVProfile                 *int `json:"dv_profile"`
	DVLevel                   *int `json:"dv_level"`
	DVVersionMajor            *int `json:"dv_version_major"`
	DVVersionMinor            *int `json:"dv_version_minor"`
	DVBLSignalCompatibilityID *int `json:"dv_bl_signal_compatibility_id"`
	RPUPresentFlag            *int `json:"rpu_present_flag"`
	ELPresentFlag             *int `json:"el_present_flag"`
	BLPresentFlag             *int `json:"bl_present_flag"`

	RedX          string `json:"red_x"`
	RedY          string `json:"red_y"`
	GreenX        string `json:"green_x"`
	GreenY        string `json:"green_y"`
	BlueX         string `json:"blue_x"`
	BlueY         string `json:"blue_y"`
	WhitePointX   string `json:"white_point_x"`
	WhitePointY   string `json:"white_point_y"`
	MinLuminance  string `json:"min_luminance"`
	MaxLuminance  string `json:"max_luminance"`
	MaxContent    *int   `json:"max_content"`
	MaxAverage    *int   `json:"max_average"`
	MaxBitrate    *int   `json:"max_bitrate"`
	AverageMaxRGB *int   `json:"average_maxrgb"`
}

// Chapter is one chapter marker.
type Chapter struct {
	ID        int64             `json:"id"`
	TimeBase  string            `json:"time_base"`
	Start     int64             `json:"start"`
	StartTime string            `json:"start_time"`
	End       int64             `json:"end"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

// Stream types as ffprobe spells them.
const (
	TypeVideo      = "video"
	TypeAudio      = "audio"
	TypeSubtitle   = "subtitle"
	TypeAttachment = "attachment"
	TypeData       = "data"
)

// SideDataDOVI is ffprobe's name for the Dolby Vision configuration record.
const SideDataDOVI = "DOVI configuration record"

// DurationSeconds is the container duration, zero when absent or unparseable.
func (f Format) DurationSeconds() float64 { return parseFloat(f.Duration) }

// SizeBytes is the container size, zero when absent or unparseable.
func (f Format) SizeBytes() int64 { return int64(parseFloat(f.Size)) }

// BitRateBPS is the overall container bitrate, zero when absent.
func (f Format) BitRateBPS() int { return int(parseFloat(f.BitRate)) }

// Tag looks a container tag up case-insensitively, because muxers disagree on the
// case of global tags and plan.md 12 has to find CODARR whichever way it comes back.
func (f Format) Tag(name string) (string, bool) { return lookupTag(f.Tags, name) }

// Tag looks a stream tag up case-insensitively.
func (s Stream) Tag(name string) (string, bool) { return lookupTag(s.Tags, name) }

func lookupTag(tags map[string]string, name string) (string, bool) {
	if v, ok := tags[name]; ok {
		return v, true
	}

	for k, v := range tags {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}

	return "", false
}

// Language is the stream's language tag, "und" when it carries none.
func (s Stream) Language() string {
	if v, ok := s.Tag("language"); ok && v != "" {
		return v
	}

	return "und"
}

// Title is the stream's title tag.
func (s Stream) Title() string {
	v, _ := s.Tag("title")

	return v
}

// BitRateBPS is the stream's own bit_rate, which Matroska almost never carries.
func (s Stream) BitRateBPS() (int, bool) {
	if s.BitRate == "" {
		return 0, false
	}

	v := int(parseFloat(s.BitRate))

	return v, v > 0
}

// BPSTagBPS is the mkvmerge BPS statistics tag, rung 2 of plan.md 8.4. The
// per-language spelling (BPS-eng) is what mkvmerge actually writes.
func (s Stream) BPSTagBPS() (int, bool) {
	if v, ok := s.Tag("BPS"); ok {
		if n := int(parseFloat(v)); n > 0 {
			return n, true
		}
	}

	for k, v := range s.Tags {
		if !strings.HasPrefix(strings.ToUpper(k), "BPS-") {
			continue
		}

		if n := int(parseFloat(v)); n > 0 {
			return n, true
		}
	}

	return 0, false
}

// IsAttachedPic reports cover art carried as a video stream. Never the primary
// video stream; see plan.md 6.2.
func (s Stream) IsAttachedPic() bool { return s.Disposition.AttachedPic == 1 }

// FrameRate prefers avg_frame_rate over r_frame_rate: r_frame_rate is the
// timebase tick rate and reads double on interlaced and telecined sources.
func (s Stream) FrameRate() float64 {
	if v := parseRational(s.AvgFrameRate); v > 0 {
		return v
	}

	return parseRational(s.RFrameRate)
}

// LevelValue normalises ffprobe's integer level: H.264 reports 41 for L4.1, HEVC
// reports general_level_idc, 30 times the level.
func (s Stream) LevelValue() (float64, bool) {
	if s.Level <= 0 {
		return 0, false
	}

	switch s.CodecName {
	case "h264":
		return float64(s.Level) / 10, true
	case "hevc":
		return float64(s.Level) / 30, true
	default:
		return 0, false
	}
}

// LevelString is the level for display and the transform record, always one
// decimal place, empty when the codec has no level.
func (s Stream) LevelString() string {
	v, ok := s.LevelValue()
	if !ok {
		return ""
	}

	return strconv.FormatFloat(v, 'f', 1, 64)
}

// Interlaced reports an explicitly interlaced field_order. Unknown and absent
// are progressive, per plan.md 6.2.
func (s Stream) Interlaced() bool {
	switch s.FieldOrder {
	case "tt", "bb", "tb", "bt":
		return true
	default:
		return false
	}
}

// FieldOrderKnown reports whether ffprobe committed to a scan type at all, which
// is what drives the idet sample for legacy codecs in plan.md 6.2.
func (s Stream) FieldOrderKnown() bool {
	switch s.FieldOrder {
	case "", "unknown":
		return false
	default:
		return true
	}
}

// IsHDR reports PQ or HLG transfer, per plan.md 9.
func (s Stream) IsHDR() bool {
	switch s.ColorTransfer {
	case "smpte2084", "arib-std-b67":
		return true
	default:
		return false
	}
}

// DolbyVisionProfile reads the profile out of the DOVI configuration record.
func (s Stream) DolbyVisionProfile() (int, bool) {
	for _, sd := range s.SideDataList {
		if !strings.EqualFold(sd.Type, SideDataDOVI) || sd.DVProfile == nil {
			continue
		}

		return *sd.DVProfile, true
	}

	return 0, false
}

// Chroma is the subsampling of the stream's pixel format.
func (s Stream) Chroma() Chroma { return ChromaOf(s.PixFmt) }

// BitDepth is bits per luma sample, 8 when the pixel format is unknown.
func (s Stream) BitDepth() int { return bitDepthOf(s.PixFmt) }

// PrimaryVideo is the first video stream that is not an attached picture; taking
// the first video stream blindly is the bug plan.md 6.2 warns about.
func (r *Result) PrimaryVideo() (Stream, bool) {
	for _, s := range r.Streams {
		if s.CodecType == TypeVideo && !s.IsAttachedPic() {
			return s, true
		}
	}

	return Stream{}, false
}

// StreamsOfType returns the streams of one codec_type in file order.
func (r *Result) StreamsOfType(codecType string) []Stream {
	var out []Stream

	for _, s := range r.Streams {
		if s.CodecType == codecType {
			out = append(out, s)
		}
	}

	return out
}

// Duration prefers the container duration and falls back to the longest stream,
// which is what a legacy container without a header duration needs.
func (r *Result) Duration() float64 {
	if d := r.Format.DurationSeconds(); d > 0 {
		return d
	}

	var longest float64
	for _, s := range r.Streams {
		if d := parseFloat(s.Duration); d > longest {
			longest = d
		}
	}

	return longest
}

func parseFloat(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}

	return v
}

// parseRational reads ffprobe's "24000/1001" form.
func parseRational(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}

	d := parseFloat(den)
	if d == 0 {
		return 0
	}

	return parseFloat(num) / d
}
