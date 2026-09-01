package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/yama6a/codarr/internal/api"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
	"github.com/yama6a/codarr/internal/promote"
)

// The adapters every package asked for but none of them owns, because owning
// one would mean depending on another (plan.md 2.2). They live here for the
// same reason the constructors do.

// prober adapts *ffprobe.CLI to promote.Prober. promote deliberately declares
// its own narrow result type so verification cannot accidentally depend on the
// whole probe.
type prober struct {
	cli *ffprobe.CLI
}

var _ promote.Prober = (*prober)(nil)

func (p *prober) Probe(ctx context.Context, path string) (promote.Output, error) {
	res, err := p.cli.Probe(ctx, path)
	if err != nil {
		return promote.Output{}, fmt.Errorf("probe %s: %w", path, err)
	}

	out := promote.Output{
		DurationSeconds: res.Duration(),
		Streams:         make([]promote.OutputStream, 0, len(res.Streams)),
	}

	for _, s := range res.Streams {
		if s.IsAttachedPic() {
			continue
		}

		stream := promote.OutputStream{
			Type:     domain.StreamType(s.CodecType),
			Codec:    s.CodecName,
			Profile:  s.Profile,
			Level:    s.LevelString(),
			Width:    s.Width,
			Height:   s.Height,
			Language: s.Language(),
		}

		if _, ok := s.DolbyVisionProfile(); ok {
			stream.DolbyVision = true
		}

		out.Streams = append(out.Streams, stream)
	}

	return out, nil
}

// analyzer adapts *ingest.Analyzer to job.Analyzer. The bulk operations of
// plan.md 19 are a re-analysis followed by an enqueue, and ingest owns the
// analysis half but speaks in paths while the queue speaks in rows.
type analyzer struct {
	inner *ingest.Analyzer
	store store.Store
}

func (a *analyzer) Analyze(ctx context.Context, m domain.MediaFile) (domain.MediaFile, error) {
	if _, err := a.inner.Analyze(ctx, m.Path, domain.OriginRecheck); err != nil {
		return domain.MediaFile{}, fmt.Errorf("re-analysing %s: %w", m.Path, err)
	}

	refreshed, err := a.store.GetMediaFile(ctx, m.ID)
	if err != nil {
		return domain.MediaFile{}, fmt.Errorf("reloading media file %d: %w", m.ID, err)
	}

	return refreshed, nil
}

// plexProvider builds the Plex client from the stored configuration and rebuilds
// it only when that configuration changes. A client per call would throw away
// the section and rating-key caches; a client built once at startup would keep
// talking to a server the operator has since edited.
type plexProvider struct {
	store  store.Store
	logger *slog.Logger

	mu      sync.Mutex
	client  *plex.Client
	fingerp string
}

func newPlexProvider(st store.Store, logger *slog.Logger) *plexProvider {
	return &plexProvider{store: st, logger: logger}
}

func (p *plexProvider) resolve(ctx context.Context) (*plex.Client, error) {
	cfg, err := p.store.GetPlexConfig(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: no plex server has been added yet", plex.ErrNotConfigured)
		}

		return nil, fmt.Errorf("read plex configuration: %w", err)
	}

	if cfg.BaseURL == "" || cfg.Token == "" {
		return nil, fmt.Errorf("%w: plex needs a base url and a token", plex.ErrNotConfigured)
	}

	mappings, err := p.store.ListPlexPathMappings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read plex path mappings: %w", err)
	}

	// The token is part of the identity but never part of a log line, so the
	// fingerprint is only ever compared, never printed.
	fp := fmt.Sprintf("%s|%s|%s|%t|%t|%v", cfg.BaseURL, cfg.Token, cfg.ClientIdentifier,
		cfg.RefreshAfter, cfg.AnalyzeAfter, mappings)

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil && p.fingerp == fp {
		return p.client, nil
	}

	client, err := plex.New(plex.Config{
		BaseURL:          cfg.BaseURL,
		Token:            cfg.Token,
		ClientIdentifier: cfg.ClientIdentifier,
		Mapper:           pathmap.New(mappings),
		RefreshAfter:     cfg.RefreshAfter,
		AnalyzeAfter:     cfg.AnalyzeAfter,
		Logger:           p.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("build plex client: %w", err)
	}

	p.client, p.fingerp = client, fp

	return client, nil
}

// APIClient is the narrow view the API layer holds.
func (p *plexProvider) APIClient(ctx context.Context) (api.PlexClient, error) {
	return p.resolve(ctx)
}

// NotifyPromoted is the Plex half of the post-promotion fan-out (16.1). A Plex
// that is not configured is not a failure: the file is already promoted and
// nothing about it depends on Plex knowing.
func (p *plexProvider) NotifyPromoted(ctx context.Context, path string) error {
	client, err := p.resolve(ctx)
	if err != nil {
		if errors.Is(err, plex.ErrNotConfigured) {
			return nil
		}

		return err
	}

	if err := client.NotifyPromoted(ctx, path); err != nil {
		return fmt.Errorf("notify plex: %w", err)
	}

	return nil
}

// IsStreaming is the guard of plan.md 15.6. It answers "not streaming" when the
// guard is switched off or Plex is not configured at all, and otherwise defers
// to the live session listing, which is deliberately never cached.
func (p *plexProvider) IsStreaming(ctx context.Context, path string) (bool, string, error) {
	cfg, err := p.store.GetPlexConfig(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("read plex configuration: %w", err)
	}

	if !cfg.GuardActiveStreams {
		return false, "", nil
	}

	client, err := p.resolve(ctx)
	if err != nil {
		if errors.Is(err, plex.ErrNotConfigured) {
			return false, "", nil
		}

		return false, "", err
	}

	streaming, who, err := client.IsStreaming(ctx, path)
	if err != nil {
		return false, "", fmt.Errorf("ask plex whether %s is being streamed: %w", path, err)
	}

	return streaming, who, nil
}

var (
	_ promote.Notifier    = (*plexProvider)(nil)
	_ promote.StreamGuard = (*plexProvider)(nil)
	_ arr.MediaServer     = (*plexProvider)(nil)
)

// arrProvider builds one client per instance, rebuilt when that instance's row
// changes. Same reasoning as plexProvider.
type arrProvider struct {
	store  store.Store
	logger *slog.Logger

	mu      sync.Mutex
	clients map[int64]*cachedArr
}

type cachedArr struct {
	client  *arr.API
	fingerp string
}

func newArrProvider(st store.Store, logger *slog.Logger) *arrProvider {
	return &arrProvider{store: st, logger: logger, clients: map[int64]*cachedArr{}}
}

func (a *arrProvider) client(ctx context.Context, instance domain.ArrInstance) (*arr.API, error) {
	mappings, err := a.store.ListArrPathMappings(ctx, instance.ID)
	if err != nil {
		return nil, fmt.Errorf("read path mappings for %s: %w", instance.Name, err)
	}

	fp := fmt.Sprintf("%s|%s|%s|%v", instance.BaseURL, instance.APIKey, instance.Flavour, mappings)

	a.mu.Lock()
	defer a.mu.Unlock()

	if cached, ok := a.clients[instance.ID]; ok && cached.fingerp == fp {
		return cached.client, nil
	}

	client, err := arr.New(arr.Config{
		Instance: instance,
		Mapper:   pathmap.New(mappings),
		Logger:   a.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("build client for %s: %w", instance.Name, err)
	}

	a.clients[instance.ID] = &cachedArr{client: client, fingerp: fp}

	return client, nil
}

// APIClient is the narrow view the API layer holds.
func (a *arrProvider) APIClient(ctx context.Context, instance domain.ArrInstance) (api.ArrClient, error) {
	return a.client(ctx, instance)
}

// ResolveOwner maps a promoted path to the instance that owns it. It reports
// false when nothing should be notified: no root, no instance on the root, or
// two enabled instances claiming it, which plan.md 16.2 says to surface rather
// than guess at.
func (a *arrProvider) ResolveOwner(ctx context.Context, path string) (arr.Owner, bool, error) {
	roots, err := a.store.ListRoots(ctx)
	if err != nil {
		return arr.Owner{}, false, fmt.Errorf("list roots: %w", err)
	}

	att, ok := pathmap.Attribute(roots, path)
	if !ok || att.Conflict != nil || att.ArrInstanceID == nil {
		return arr.Owner{}, false, nil
	}

	instance, err := a.store.GetArrInstance(ctx, *att.ArrInstanceID)
	if err != nil {
		return arr.Owner{}, false, fmt.Errorf("load arr instance %d: %w", *att.ArrInstanceID, err)
	}

	if !instance.Enabled {
		return arr.Owner{}, false, nil
	}

	client, err := a.client(ctx, instance)
	if err != nil {
		return arr.Owner{}, false, err
	}

	return arr.Owner{
		Client:         client,
		Item:           a.itemFor(ctx, instance, path),
		RescanAfter:    instance.RescanAfter,
		UnmonitorAfter: instance.UnmonitorAfter,
	}, true, nil
}

// itemFor reads the entity id a webhook recorded on the row. A file that only a
// scan has ever seen carries none, and the rescan then names nothing, which the
// *arrs treat as a full-library rescan rather than an error.
func (a *arrProvider) itemFor(ctx context.Context, instance domain.ArrInstance, path string) arr.ItemRef {
	media, err := a.store.GetMediaFileByPath(ctx, path)
	if err != nil || media.ArrEntityID == nil {
		return arr.ItemRef{}
	}

	if instance.Flavour == domain.FlavourRadarr {
		return arr.ItemRef{MovieID: *media.ArrEntityID}
	}

	return arr.ItemRef{SeriesID: *media.ArrEntityID}
}

var _ arr.OwnerResolver = (*arrProvider)(nil)
