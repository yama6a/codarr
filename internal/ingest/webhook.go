package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"

	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// EventType is the *arr eventType field. Anything not listed is acknowledged
// and ignored (plan.md 13.1).
type EventType string

// The events Codarr acts on.
const (
	EventDownload          EventType = "Download"
	EventRename            EventType = "Rename"
	EventTest              EventType = "Test"
	EventMovieFileDelete   EventType = "MovieFileDelete"
	EventEpisodeFileDelete EventType = "EpisodeFileDelete"
)

// Event is an *arr webhook payload reduced to the fields plan.md 13.1 reads.
// internal/arr owns the JSON and the Radarr/Sonarr differences; this is the
// shape it hands over.
type Event struct {
	Type EventType

	// EntityID is movie.id or series.id, stored on the media row so a rescan
	// after promotion knows what to name (16.2).
	EntityID *int64

	Title string

	// FolderPath is movie.folderPath or series.path, as that instance sees it.
	// A Rename carries no per-file paths, so the folder is what gets walked.
	FolderPath string

	// Paths are the file paths as that instance sees them. VERIFY.md: every
	// live instance reports /media, so remapping is mandatory, not optional.
	Paths []string

	IsUpgrade bool
}

// Ack is what the *arr's Connect gets back. Test must succeed for the Test
// button to report success, so an unhandled event is still an ack.
type Ack struct {
	Received bool
	Message  string
	Instance string

	// Results is one entry per path that reached analysis.
	Results []Result

	// MarkedMissing is the media_files ids a delete event retired.
	MarkedMissing []int64
}

// Webhook turns one parsed *arr event into work. Dispatch is by webhook_id, one
// per instance, so nothing has to guess which instance sent an event.
type Webhook struct {
	store    WebhookStore
	analyzer FileAnalyzer
	fs       FS
	logger   *slog.Logger
}

// NewWebhook returns a Webhook.
func NewWebhook(st WebhookStore, analyzer FileAnalyzer, fs FS, logger *slog.Logger) *Webhook {
	return &Webhook{
		store:    st,
		analyzer: analyzer,
		fs:       fs,
		logger:   logger.With(slog.String("component", "ingest.webhook")),
	}
}

// Handle attributes the event to the instance owning webhookID, remaps its
// paths through that instance's own mappings, and enqueues the work.
func (w *Webhook) Handle(ctx context.Context, webhookID string, ev Event) (Ack, error) {
	instance, err := w.store.GetArrInstanceByWebhookID(ctx, webhookID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Ack{}, fmt.Errorf("%w: %s", ErrUnknownWebhook, webhookID)
		}

		return Ack{}, fmt.Errorf("look up webhook %s: %w", webhookID, err)
	}

	ack := Ack{Received: true, Instance: instance.Name}

	if ev.Type == EventTest {
		ack.Message = "Codarr received the test from " + instance.Name

		return ack, nil
	}

	if !instance.Enabled {
		ack.Message = instance.Name + " is disabled in Codarr, so the event was ignored"

		return ack, nil
	}

	switch ev.Type {
	case EventDownload, EventRename:
		return w.ingest(ctx, instance, ev, ack)
	case EventMovieFileDelete, EventEpisodeFileDelete:
		return w.retire(ctx, instance, ev, ack)
	case EventTest:
		return ack, nil
	default:
		ack.Message = "event type " + string(ev.Type) + " is not one Codarr acts on"

		return ack, nil
	}
}

func (w *Webhook) ingest(ctx context.Context, instance domain.ArrInstance, ev Event, ack Ack) (Ack, error) {
	paths, env, err := w.resolve(ctx, instance, ev)
	if err != nil {
		return ack, err
	}

	if len(paths) == 0 {
		ack.Message = "no file paths in the payload that map into Codarr's view of the filesystem"

		return ack, nil
	}

	for _, p := range paths {
		res, err := w.analyzer.AnalyzeIn(ctx, p, env)
		if err != nil {
			// One bad file does not fail the webhook: the *arr would retry the
			// whole event, and the daily scan covers what is missed anyway.
			w.logger.Error("webhook analysis failed",
				slog.String("instance", instance.Name),
				slog.String("path", p), slog.String("error", err.Error()))

			continue
		}

		ack.Results = append(ack.Results, res)
	}

	ack.Message = fmt.Sprintf("%s: analysed %d of %d files", instance.Name, len(ack.Results), len(paths))

	return ack, nil
}

// retire handles MovieFileDelete and EpisodeFileDelete. The row and its history
// are kept; only the status changes (13.2).
func (w *Webhook) retire(ctx context.Context, instance domain.ArrInstance, ev Event, ack Ack) (Ack, error) {
	paths, _, err := w.resolve(ctx, instance, ev)
	if err != nil {
		return ack, err
	}

	var ids []int64

	for _, p := range paths {
		row, err := w.store.GetMediaFileByPath(ctx, p)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return ack, fmt.Errorf("look up %s: %w", p, err)
			}

			continue
		}

		ids = append(ids, row.ID)
	}

	if len(ids) > 0 {
		if _, err := w.store.MarkMediaMissing(ctx, ids); err != nil {
			return ack, fmt.Errorf("mark %d files missing: %w", len(ids), err)
		}
	}

	ack.MarkedMissing = ids
	ack.Message = fmt.Sprintf("%s: marked %d files missing", instance.Name, len(ids))

	return ack, nil
}

// resolve maps the event's paths into Codarr's view and loads the per-pass
// context. A Rename carries no per-file paths, so the folder is walked.
func (w *Webhook) resolve(ctx context.Context, instance domain.ArrInstance, ev Event) ([]string, Env, error) {
	mappings, err := w.store.ListArrPathMappings(ctx, instance.ID)
	if err != nil {
		return nil, Env{}, fmt.Errorf("list path mappings for %s: %w", instance.Name, err)
	}

	roots, err := w.store.ListRoots(ctx)
	if err != nil {
		return nil, Env{}, fmt.Errorf("list roots: %w", err)
	}

	settings, err := w.store.GetSettings(ctx)
	if err != nil {
		return nil, Env{}, fmt.Errorf("get settings: %w", err)
	}

	env := Env{
		Roots:       roots,
		Settings:    settings,
		Origin:      domain.OriginIngest,
		ArrEntityID: ev.EntityID,
	}
	mapper := pathmap.New(mappings)

	paths := w.mapPaths(instance, mapper, ev.Paths)
	if len(paths) == 0 && ev.FolderPath != "" {
		paths = w.walkFolder(instance, mapper, ev.FolderPath)
	}

	return paths, env, nil
}

func (w *Webhook) mapPaths(instance domain.ArrInstance, mapper *pathmap.Mapper, remote []string) []string {
	out := make([]string, 0, len(remote))

	for _, r := range remote {
		if pathmap.Normalise(r) == "" {
			w.logger.Warn("webhook path is not absolute, ignoring",
				slog.String("instance", instance.Name), slog.String("path", r))

			continue
		}

		local, mapped := mapper.ToLocal(r)

		if !mapped {
			// VERIFY.md: every live instance reports /media. An unmapped path
			// is a missing mapping, and processing it would attribute the file
			// to the wrong instance or to none.
			w.logger.Warn("webhook path has no mapping for this instance",
				slog.String("instance", instance.Name), slog.String("path", r))
		}

		out = append(out, local)
	}

	return out
}

func (w *Webhook) walkFolder(instance domain.ArrInstance, mapper *pathmap.Mapper, remote string) []string {
	if pathmap.Normalise(remote) == "" {
		return nil
	}

	local, mapped := mapper.ToLocal(remote)

	if !mapped {
		w.logger.Warn("webhook folder has no mapping for this instance",
			slog.String("instance", instance.Name), slog.String("path", remote))
	}

	var out []string

	err := w.fs.WalkDir(local, func(path string, info fsx.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // a partial folder is still worth ingesting; the daily scan catches the rest
		}

		if info.IsDir {
			if path != local && ExcludeDir(filepath.Base(path)) {
				return fs.SkipDir
			}

			return nil
		}

		if ExcludeFile(path, info.Size) == NotExcluded {
			out = append(out, path)
		}

		return nil
	})
	if err != nil {
		w.logger.Error("could not walk the renamed folder",
			slog.String("instance", instance.Name),
			slog.String("path", local), slog.String("error", err.Error()))
	}

	return out
}
