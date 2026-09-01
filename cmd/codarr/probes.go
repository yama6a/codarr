package main

import (
	"context"

	"github.com/yama6a/codarr/internal/pkg/metrics"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// The two series of plan.md 24 that need a live dependency. They run on the
// refresh tick, never on a scrape, so an unreachable Plex slows only the refresh.

func plexProbe(plexes *plexProvider) metrics.Probe {
	return func(ctx context.Context, m *metrics.Metrics) {
		client, err := plexes.resolve(ctx)
		if err != nil {
			m.SetPlex(false, 0)

			return
		}

		sessions, err := client.Sessions(ctx)
		if err != nil {
			m.SetPlex(false, 0)

			return
		}

		m.SetPlex(true, len(sessions))
	}
}

func arrProbe(st store.Store, arrs *arrProvider) metrics.Probe {
	return func(ctx context.Context, m *metrics.Metrics) {
		instances, err := st.ListArrInstances(ctx)
		if err != nil {
			return
		}

		for _, in := range instances {
			if !in.Enabled {
				// A disabled instance stops reporting rather than reporting down and paging someone.
				m.ForgetArr(in.Name)

				continue
			}

			client, err := arrs.client(ctx, in)
			if err != nil {
				m.SetArrUp(in.Name, false)

				continue
			}

			m.SetArrUp(in.Name, client.Test(ctx).OK)
		}
	}
}
