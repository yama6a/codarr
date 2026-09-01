package decide

import "errors"

// ErrNoVideoStream is returned for a file whose only video streams are cover
// art, or which has none at all.
var ErrNoVideoStream = errors.New("decide: no video stream")

// ErrNoAudioStreams is returned when executing the plan would write a file with
// no audio, which plan.md 6.3 forbids outright.
var ErrNoAudioStreams = errors.New("decide: mapping would produce no audio streams")

// ErrNoProbe is returned when Plan is called without a probe result.
var ErrNoProbe = errors.New("decide: no probe result")
