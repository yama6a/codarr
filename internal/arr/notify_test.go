package arr_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/arr/mock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/promote"
)

var _ promote.Notifier = (*arr.Notifier)(nil)

const promoted = "/media/yama/movies/Arrival (2016)/Arrival (2016) Bluray-1080p.mkv"

func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func clientMock() *mock.ClientMock {
	return &mock.ClientMock{
		IdentityFunc:  func() arr.Identity { return arr.Identity{ID: 1, Name: "radarr-yama", Flavour: domain.FlavourRadarr} },
		RescanFunc:    func(context.Context, arr.ItemRef) error { return nil },
		UnmonitorFunc: func(context.Context, arr.ItemRef) error { return nil },
	}
}

func resolverFor(owner arr.Owner, ok bool, err error) *mock.OwnerResolverMock {
	return &mock.OwnerResolverMock{
		ResolveOwnerFunc: func(context.Context, string) (arr.Owner, bool, error) { return owner, ok, err },
	}
}

func TestNotifyPromoted_RefreshesPlexAndRescansTheOwningInstance(t *testing.T) {
	t.Parallel()

	plex := &mock.MediaServerMock{NotifyPromotedFunc: func(context.Context, string) error { return nil }}
	client := clientMock()
	owner := arr.Owner{Client: client, Item: arr.ItemRef{MovieID: 412}, RescanAfter: true}

	n := arr.NewNotifier(plex, resolverFor(owner, true, nil), quietLogger())
	require.NoError(t, n.NotifyPromoted(context.Background(), promoted))

	require.Len(t, plex.NotifyPromotedCalls(), 1)
	require.Equal(t, promoted, plex.NotifyPromotedCalls()[0].Path)
	require.Equal(t, []arr.ItemRef{{MovieID: 412}}, rescanItems(client))
	require.Empty(t, client.UnmonitorCalls())
}

// unmonitor_after ships off, so nothing is unmonitored unless the instance
// asked for it (plan.md 16.2).
func TestNotifyPromoted_OnlyUnmonitorsWhenTheInstanceAsksForIt(t *testing.T) {
	t.Parallel()

	client := clientMock()
	owner := arr.Owner{Client: client, Item: arr.ItemRef{MovieID: 412}, RescanAfter: true, UnmonitorAfter: true}

	n := arr.NewNotifier(nil, resolverFor(owner, true, nil), quietLogger())
	require.NoError(t, n.NotifyPromoted(context.Background(), promoted))

	require.Len(t, client.UnmonitorCalls(), 1)
}

// A Plex that is down must not stop Radarr being told, and vice versa. Both
// failures come back joined so the job records one warning naming each.
func TestNotifyPromoted_TellsTheArrEvenWhenPlexFails(t *testing.T) {
	t.Parallel()

	plex := &mock.MediaServerMock{
		NotifyPromotedFunc: func(context.Context, string) error { return errors.New("plex is down") },
	}
	client := clientMock()
	owner := arr.Owner{Client: client, Item: arr.ItemRef{MovieID: 412}, RescanAfter: true}

	n := arr.NewNotifier(plex, resolverFor(owner, true, nil), quietLogger())

	err := n.NotifyPromoted(context.Background(), promoted)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plex is down")
	require.Len(t, client.RescanCalls(), 1)
}

func TestNotifyPromoted_RefreshesPlexEvenWhenTheRescanFails(t *testing.T) {
	t.Parallel()

	plex := &mock.MediaServerMock{NotifyPromotedFunc: func(context.Context, string) error { return nil }}
	client := clientMock()
	client.RescanFunc = func(context.Context, arr.ItemRef) error { return errors.New("radarr is down") }

	owner := arr.Owner{Client: client, Item: arr.ItemRef{MovieID: 412}, RescanAfter: true, UnmonitorAfter: true}

	n := arr.NewNotifier(plex, resolverFor(owner, true, nil), quietLogger())

	err := n.NotifyPromoted(context.Background(), promoted)
	require.Error(t, err)
	require.Contains(t, err.Error(), "radarr is down")
	require.Len(t, plex.NotifyPromotedCalls(), 1)
	require.Len(t, client.UnmonitorCalls(), 1)
}

// A root with no instance, or a root two enabled instances both claim: plan.md
// 16.2 says process the file and notify nobody rather than guess an owner.
func TestNotifyPromoted_NotifiesNoArrWhenNoneOwnsThePath(t *testing.T) {
	t.Parallel()

	plex := &mock.MediaServerMock{NotifyPromotedFunc: func(context.Context, string) error { return nil }}

	n := arr.NewNotifier(plex, resolverFor(arr.Owner{}, false, nil), quietLogger())
	require.NoError(t, n.NotifyPromoted(context.Background(), promoted))
	require.Len(t, plex.NotifyPromotedCalls(), 1)
}

func TestNotifyPromoted_ReportsAFailedOwnerLookup(t *testing.T) {
	t.Parallel()

	n := arr.NewNotifier(nil, resolverFor(arr.Owner{}, false, errors.New("db is gone")), quietLogger())

	err := n.NotifyPromoted(context.Background(), promoted)
	require.Error(t, err)
	require.Contains(t, err.Error(), "db is gone")
}

func TestNotifyPromoted_DoesNothingWithNeitherHalfConfigured(t *testing.T) {
	t.Parallel()

	require.NoError(t, arr.NewNotifier(nil, nil, quietLogger()).NotifyPromoted(context.Background(), promoted))
}

func rescanItems(c *mock.ClientMock) []arr.ItemRef {
	out := make([]arr.ItemRef, 0, len(c.RescanCalls()))
	for _, call := range c.RescanCalls() {
		out = append(out, call.Item)
	}

	return out
}
