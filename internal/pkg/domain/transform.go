package domain

import "time"

// TransformRecord is the before/after history for one job, never deleted.
//
// "after" holds the plan at enqueue and the probed output at completion, one schema
// for both so the UI renders queued and completed items with the same component.
type TransformRecord struct {
	Container   BeforeAfterString   `json:"container"`
	Video       VideoTransform      `json:"video"`
	Audio       []AudioTransform    `json:"audio"`
	Subtitles   []SubtitleTransform `json:"subtitles"`
	Attachments BeforeAfterInt      `json:"attachments"`
	Chapters    BeforeAfterInt      `json:"chapters"`
	Size        SizeTransform       `json:"size"`
	Duration    DurationTransform   `json:"duration_seconds"`

	// Nil until promotion, then immutable. The media row's copy is the mutable
	// view, and the two diverging is the signal that something rewrote the file.
	OutputIdentity *OutputIdentity `json:"output_identity"`
}

type BeforeAfterString struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type BeforeAfterInt struct {
	Before int `json:"before"`
	After  int `json:"after"`
}

type SizeTransform struct {
	BeforeBytes int64 `json:"before_bytes"`
	AfterBytes  int64 `json:"after_bytes"`
}

type DurationTransform struct {
	Estimated int  `json:"estimated"`
	Actual    *int `json:"actual"`
}

type VideoTransform struct {
	Action Decision    `json:"action"`
	Reason string      `json:"reason"`
	Before *VideoState `json:"before"`
	After  *VideoState `json:"after"`
}

type VideoState struct {
	Codec       string  `json:"codec"`
	Profile     string  `json:"profile"`
	Level       string  `json:"level"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         float64 `json:"fps"`
	BitrateKbps *int    `json:"bitrate_kbps"`
	PixFmt      string  `json:"pix_fmt"`
	HDR         bool    `json:"hdr"`
	Scan        Scan    `json:"scan"`
}

type AudioTransform struct {
	SourceIndex int         `json:"source_index"`
	OutputIndex *int        `json:"output_index"`
	Language    string      `json:"language"`
	Title       *string     `json:"title"`
	Action      Decision    `json:"action"`
	Reason      string      `json:"reason,omitempty"`
	Before      *AudioState `json:"before"`
	After       *AudioState `json:"after"`
}

type AudioState struct {
	Codec       string `json:"codec"`
	Profile     string `json:"profile,omitempty"`
	Channels    int    `json:"channels"`
	Layout      string `json:"layout"`
	BitrateKbps *int   `json:"bitrate_kbps"`
}

type SubtitleTransform struct {
	SourceIndex int            `json:"source_index"`
	OutputIndex *int           `json:"output_index"`
	Language    string         `json:"language"`
	Action      Decision       `json:"action"`
	Reason      string         `json:"reason,omitempty"`
	Before      *SubtitleState `json:"before"`
	After       *SubtitleState `json:"after"`
}

type SubtitleState struct {
	Codec  string `json:"codec"`
	Forced bool   `json:"forced"`
}

// OutputIdentity is written at promotion, after the mtime restore, since
// restoring mtime changes what a later scan compares against.
type OutputIdentity struct {
	Fingerprint string    `json:"fingerprint"`
	FullHash    *string   `json:"full_hash"`
	SizeBytes   int64     `json:"size_bytes"`
	MTime       int64     `json:"mtime"`
	PolicyHash  string    `json:"policy_hash"`
	RecordedAt  time.Time `json:"recorded_at"`
}
