package promote_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/promote"
)

// fakeMetrics collects the categories promotion recorded.
type fakeMetrics struct {
	mu         sync.Mutex
	categories []string
}

var _ promote.Metrics = (*fakeMetrics)(nil)

func (f *fakeMetrics) Error(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.categories = append(f.categories, category)
}

func (f *fakeMetrics) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.categories...)
}

func (h *harness) meter() *fakeMetrics {
	h.t.Helper()

	mx := &fakeMetrics{}
	h.rebuild(func(d *promote.Deps) { d.Metrics = mx })

	return mx
}

// plan.md 15.6 turns an unanswerable Plex into a deferral, which is the right
// call and also completely silent: the job waits and nothing ever fails.
func TestPromoter_MetricsRecordAPlexThatCannotBeAsked(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	mx := h.meter()

	asked := 0
	h.guard.IsStreamingFunc = func(context.Context, string) (bool, string, error) {
		asked++
		if asked == 1 {
			return false, "", errors.New("dial tcp 10.0.0.5:32400: connect: connection refused")
		}

		return false, "", nil
	}

	var blocked []string

	req := request()
	req.OnBlocked = func(reason string) { blocked = append(blocked, reason) }

	res, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err, "an unanswerable Plex defers and retries, it never fails the job")
	require.True(t, res.Renamed)
	require.Len(t, blocked, 1)
	require.Equal(t, []string{promote.ErrorStreamGuard}, mx.recorded())
}

func TestPromoter_MetricsRecordAFailedPostPromotionNotification(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	mx := h.meter()

	h.notifier.NotifyPromotedFunc = func(context.Context, string) error {
		return errors.New("radarr-4k returned 503")
	}

	res, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err, "the source is already gone, so a notification failure is only a warning")
	require.True(t, res.Renamed)
	require.Len(t, res.Warnings, 1)
	require.Equal(t, []string{promote.ErrorNotify}, mx.recorded())
}

func TestPromoter_MetricsAreOptional(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.notifier.NotifyPromotedFunc = func(context.Context, string) error {
		return errors.New("radarr-4k returned 503")
	}

	res, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)
	require.True(t, res.Renamed)
}
