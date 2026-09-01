package main

import (
	"context"

	"github.com/yama6a/codarr/internal/pkg/metrics"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// The two series in plan.md 24 that need a live dependency rather than the
// database. They run on the metrics refresh tick, never on a scrape, so a Plex
// that has gone away slows nothing down but the refresh itself.

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
				// A disabled instance stops reporting rather than reporting
				// down, which would page someone for a deliberate change.
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
