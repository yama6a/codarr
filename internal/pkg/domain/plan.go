package domain

// StreamPlan is the decision for one source stream, plus where it lands in the output.
//
// Both indices are carried because ffmpeg's -c:a:N and friends address the output
// position, not the source. OutputIndex is nil for a dropped stream.
type StreamPlan struct {
	Type        StreamType `json:"type"`
	SourceIndex int        `json:"source_index"`
	OutputIndex *int       `json:"output_index"`
	Decision    Decision   `json:"decision"`
	Reason      string     `json:"reason"`

	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`

	TargetCodec    string `json:"target_codec,omitempty"`
	TargetBitrate  int    `json:"target_bitrate_bps,omitempty"`
	TargetChannels int    `json:"target_channels,omitempty"`

	Default        bool `json:"default"`
	Forced         bool `json:"forced"`
	Comment        bool `json:"comment"`
	VisualImpaired bool `json:"visual_impaired"`
}

// Plan is the whole per-file decision. It is derived purely from an ffprobe
// result plus the hard-coded policy, so it is deterministic and testable.
type Plan struct {
	Kind Kind `json:"kind"`

	SourceContainer string    `json:"source_container"`
	OutputContainer Container `json:"output_container"`

	Streams []StreamPlan `json:"streams"`

	// Set when an H.264 stream fails only the level test and fits the carve-out of
	// plan.md 6.2, whose bounds live in internal/decide. The stream is still copied.
	LevelRewrite bool `json:"level_rewrite"`

	// Deinterlace is set for explicitly interlaced sources, or for legacy
	// codecs where the idet sample said so.
	Deinterlace bool `json:"deinterlace"`

	HDR bool `json:"hdr"`

	// The source color_transfer, because plan.md 9 prescribes -color_trc smpte2084
	// unconditionally and that renders an HLG source wrong. Empty means PQ.
	HDRTransfer        string `json:"hdr_transfer,omitempty"`
	DolbyVision        bool   `json:"dolby_vision"`
	DolbyVisionProfile int    `json:"dolby_vision_profile,omitempty"`

	// TargetVideoBitrate is zero until the sample probe has run, which happens
	// as the first phase of a full job rather than at enqueue time.
	TargetVideoBitrate int `json:"target_video_bitrate_bps,omitempty"`

	Reasons    []string `json:"reasons"`
	PolicyHash string   `json:"policy_hash"`
}

// NeedsWrite reports whether executing this plan produces a new file.
func (p Plan) NeedsWrite() bool { return p.Kind != KindSkip }

// VideoStream returns the plan for the primary video stream, which is the first
// video stream that is not an attached picture.
func (p Plan) VideoStream() (StreamPlan, bool) {
	for _, s := range p.Streams {
		if s.Type == StreamVideo && s.Decision != DecisionDrop {
			return s, true
		}
	}

	return StreamPlan{}, false
}
