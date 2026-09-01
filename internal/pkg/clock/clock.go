// Package clock is the time boundary, so anything that waits or timestamps can
// be tested without sleeping.
package clock

import "time"

//go:generate go run -mod=mod github.com/matryer/moq -out mock/clock_mock.go -pkg mock . Clock

type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

var _ Clock = systemClock{}

func System() Clock { return systemClock{} }

func (systemClock) Now() time.Time                         { return time.Now().UTC() }
func (systemClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
