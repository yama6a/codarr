package clock

import (
	"sync"
	"time"
)

// Fake is a hand-written Clock for tests that need to advance time rather than
// stub individual calls. Prefer the generated mock when a single Now() will do.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

var _ Clock = (*Fake)(nil)

func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.now
}

func (f *Fake) Since(t time.Time) time.Duration { return f.Now().Sub(t) }

// After fires immediately: a test that advances time itself should not also
// wait on a real timer.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.Advance(d)
	ch := make(chan time.Time, 1)
	ch <- f.Now()

	return ch
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
