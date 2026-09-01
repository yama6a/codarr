package decide_test

import (
	"github.com/yama6a/codarr/internal/ffprobe"
)

type opt func(*ffprobe.Stream)

func withPixFmt(f string) opt     { return func(s *ffprobe.Stream) { s.PixFmt = f } }
func withFieldOrder(f string) opt { return func(s *ffprobe.Stream) { s.FieldOrder = f } }
func withRefs(n int) opt          { return func(s *ffprobe.Stream) { s.Refs = n } }
func withLang(l string) opt       { return func(s *ffprobe.Stream) { setTag(s, "language", l) } }
func withTitle(t string) opt      { return func(s *ffprobe.Stream) { setTag(s, "title", t) } }
func withBitRate(b string) opt    { return func(s *ffprobe.Stream) { s.BitRate = b } }
func withProfile(p string) opt    { return func(s *ffprobe.Stream) { s.Profile = p } }
func withLayout(l string) opt     { return func(s *ffprobe.Stream) { s.ChannelLayout = l } }
func withLevel(l int) opt         { return func(s *ffprobe.Stream) { s.Level = l } }
func withBPSTag(b string) opt     { return func(s *ffprobe.Stream) { setTag(s, "BPS-eng", b) } }

func withSize(w, h int) opt {
	return func(s *ffprobe.Stream) { s.Width, s.Height = w, h }
}

func withFPS(rate string) opt {
	return func(s *ffprobe.Stream) { s.AvgFrameRate, s.RFrameRate = rate, rate }
}

func withHDR() opt {
	return func(s *ffprobe.Stream) {
		s.ColorTransfer = "smpte2084"
		s.ColorPrimaries = "bt2020"
		s.ColorSpace = "bt2020nc"
	}
}

func withDolbyVision(profile int) opt {
	return func(s *ffprobe.Stream) {
		s.SideDataList = append(s.SideDataList, ffprobe.SideData{
			Type:      ffprobe.SideDataDOVI,
			DVProfile: &profile,
		})
	}
}

func withAttachedPic() opt {
	return func(s *ffprobe.Stream) { s.Disposition.AttachedPic = 1 }
}

func withForced() opt {
	return func(s *ffprobe.Stream) { s.Disposition.Forced = 1 }
}

func setTag(s *ffprobe.Stream, k, v string) {
	if s.Tags == nil {
		s.Tags = map[string]string{}
	}

	s.Tags[k] = v
}

// video is a progressive 1080p stream, the shape most of the library has.
func video(codec, profile string, opts ...opt) ffprobe.Stream {
	s := ffprobe.Stream{
		CodecType:    ffprobe.TypeVideo,
		CodecName:    codec,
		Profile:      profile,
		Level:        40,
		Width:        1920,
		Height:       1080,
		PixFmt:       "yuv420p",
		Refs:         3,
		AvgFrameRate: "24000/1001",
		RFrameRate:   "24000/1001",
		Disposition:  ffprobe.Disposition{Default: 1},
	}

	if codec == "hevc" {
		s.Level = 120
	}

	if codec != "h264" && codec != "hevc" {
		s.Level = -99
	}

	for _, o := range opts {
		o(&s)
	}

	return s
}

func audio(codec string, channels int, opts ...opt) ffprobe.Stream {
	s := ffprobe.Stream{
		CodecType:     ffprobe.TypeAudio,
		CodecName:     codec,
		Channels:      channels,
		ChannelLayout: layoutFor(channels),
		SampleRate:    "48000",
		Level:         -99,
		Disposition:   ffprobe.Disposition{Default: 1},
		Tags:          map[string]string{"language": "eng"},
	}

	for _, o := range opts {
		o(&s)
	}

	return s
}

func layoutFor(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1(side)"
	case 8:
		return "7.1"
	default:
		return ""
	}
}

func subtitle(codec string, opts ...opt) ffprobe.Stream {
	s := ffprobe.Stream{
		CodecType:   ffprobe.TypeSubtitle,
		CodecName:   codec,
		Level:       -99,
		Disposition: ffprobe.Disposition{},
		Tags:        map[string]string{"language": "eng"},
	}

	for _, o := range opts {
		o(&s)
	}

	return s
}

func attachment(name string) ffprobe.Stream {
	return ffprobe.Stream{
		CodecType: ffprobe.TypeAttachment,
		CodecName: "ttf",
		Level:     -99,
		Tags:      map[string]string{"filename": name},
	}
}

// probeOf assembles a result, numbering the global stream indices the way
// ffprobe does.
func probeOf(formatName string, streams ...ffprobe.Stream) *ffprobe.Result {
	r := &ffprobe.Result{
		Format: ffprobe.Format{
			FormatName: formatName,
			Duration:   "7200.000000",
			Size:       "9000000000",
			BitRate:    "10000000",
		},
	}

	for i, s := range streams {
		s.Index = i
		r.Streams = append(r.Streams, s)
	}

	r.Format.NBStreams = len(r.Streams)

	return r
}

func mkv(streams ...ffprobe.Stream) *ffprobe.Result {
	return probeOf("matroska,webm", streams...)
}

func mp4(streams ...ffprobe.Stream) *ffprobe.Result {
	return probeOf("mov,mp4,m4a,3gp,3g2,mj2", streams...)
}
