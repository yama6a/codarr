package arr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/yama6a/codarr/internal/promote"
)

// MediaServer is the Plex half of the post-promotion notification, narrowed to
// the one call promotion makes. *plex.Client satisfies it.
type MediaServer interface {
	NotifyPromoted(ctx context.Context, path string) error
}

// Owner is the instance that owns a promoted path and what to tell it; the flags come
// from the instance row so the decision to notify is visible at the call site.
type Owner struct {
	Client         Client
	Item           ItemRef
	RescanAfter    bool
	UnmonitorAfter bool
}

// OwnerResolver maps a promoted path to its owning instance, reporting false when no
// instance or two claim it, which plan.md 16.2 says not to guess at.
type OwnerResolver interface {
	ResolveOwner(ctx context.Context, path string) (Owner, bool, error)
}

// Notifier composes the Plex refresh with the owning instance's rescan (plan.md 16.1,
// 16.2). It runs after the source file is gone, so its errors are warnings, not failures.
type Notifier struct {
	plex     MediaServer
	resolver OwnerResolver
	log      *slog.Logger
}

var _ promote.Notifier = (*Notifier)(nil)

// NewNotifier returns the composed notifier.
func NewNotifier(plex MediaServer, resolver OwnerResolver, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}

	return &Notifier{plex: plex, resolver: resolver, log: log}
}

// NotifyPromoted refreshes Plex and rescans the owning instance, neither half
// short-circuiting the other.
func (n *Notifier) NotifyPromoted(ctx context.Context, path string) error {
	var failures []error

	if n.plex != nil {
		if err := n.plex.NotifyPromoted(ctx, path); err != nil {
			failures = append(failures, fmt.Errorf("notifying plex about %s failed: %w", path, err))
			n.log.WarnContext(ctx, "notifying plex after a promotion failed",
				slog.String("path", path), slog.Any("error", err))
		}
	}

	if err := n.notifyArr(ctx, path); err != nil {
		failures = append(failures, err)
	}

	return errors.Join(failures...)
}

func (n *Notifier) notifyArr(ctx context.Context, path string) error {
	if n.resolver == nil {
		return nil
	}

	owner, ok, err := n.resolver.ResolveOwner(ctx, path)
	if err != nil {
		n.log.WarnContext(ctx, "resolving the owning *arr instance failed",
			slog.String("path", path), slog.Any("error", err))

		return fmt.Errorf("resolving the *arr instance owning %s failed: %w", path, err)
	}

	if !ok || owner.Client == nil {
		n.log.InfoContext(ctx, "no *arr instance owns the promoted path, nothing to notify",
			slog.String("path", path))

		return nil
	}

	return n.tell(ctx, path, owner)
}

func (n *Notifier) tell(ctx context.Context, path string, owner Owner) error {
	id := owner.Client.Identity()

	var failures []error

	if owner.RescanAfter {
		if err := owner.Client.Rescan(ctx, owner.Item); err != nil {
			failures = append(failures, fmt.Errorf("rescanning %s on %s failed: %w", path, id.Name, err))
			n.log.WarnContext(ctx, "rescan after a promotion failed",
				slog.String("path", path), slog.String("instance", id.Name), slog.Any("error", err))
		}
	}

	// Unmonitoring runs even when the rescan failed: the two are independent,
	// and this is the one that stops a re-grab (plan.md 16.2).
	if owner.UnmonitorAfter {
		if err := owner.Client.Unmonitor(ctx, owner.Item); err != nil {
			failures = append(failures, fmt.Errorf("unmonitoring %s on %s failed: %w", path, id.Name, err))
			n.log.WarnContext(ctx, "unmonitor after a promotion failed",
				slog.String("path", path), slog.String("instance", id.Name), slog.Any("error", err))
		}
	}

	return errors.Join(failures...)
}
