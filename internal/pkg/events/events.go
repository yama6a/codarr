// Package events is Codarr's logging. plan.md 24: log/slog with a JSON handler
// writing to stdout, wrapped so records at info and above are also written to
// the events table for the UI's log view.
//
// Stdout is the source of truth. A failure writing the row must never stop the
// stdout line being emitted, which is why the wrapper calls the inner handler
// first and never propagates the table's error.
package events

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// SinkTimeout bounds one events-table insert. The write pool holds a single
// connection, so an insert that cannot get it has to give up rather than block
// the goroutine that was only trying to log.
const SinkTimeout = 5 * time.Second

// DefaultCategory is used for a record carrying no component or category attr.
const DefaultCategory = "app"

// Store is the events table. store.Store satisfies it.
type Store interface {
	AppendEvent(ctx context.Context, e domain.Event) (int64, error)
}

// Options configures the logger. Everything except Level has a usable default.
type Options struct {
	// Out is where the JSON lines go. Defaults to os.Stdout, which plan.md 24
	// names as the source of truth.
	Out io.Writer

	// Level is the floor for stdout.
	Level slog.Level

	// Store is the events table sink. A nil Store logs to stdout only.
	Store Store

	Clock clock.Clock

	// TableLevel is the floor for the events table, info by default (24).
	TableLevel slog.Level

	// OnSinkError sees every failed table insert. The default writes one line
	// to stderr, which cannot recurse back into this handler.
	OnSinkError func(error)

	// AddSource stamps the call site on every record.
	AddSource bool
}

// New returns the logger the whole binary uses.
func New(o Options) *slog.Logger {
	return slog.New(NewHandler(o))
}

// NewHandler returns the JSON handler with the events-table sink wrapped around
// it.
func NewHandler(o Options) slog.Handler {
	if o.Out == nil {
		o.Out = os.Stdout
	}

	if o.Clock == nil {
		o.Clock = clock.System()
	}

	if o.TableLevel == 0 {
		o.TableLevel = slog.LevelInfo
	}

	if o.OnSinkError == nil {
		o.OnSinkError = stderrSinkError
	}

	inner := slog.NewJSONHandler(o.Out, &slog.HandlerOptions{
		Level:       o.Level,
		AddSource:   o.AddSource,
		ReplaceAttr: redact,
	})

	if o.Store == nil {
		return inner
	}

	return &handler{
		inner:      inner,
		store:      o.Store,
		clk:        o.Clock,
		tableLevel: o.TableLevel,
		onErr:      o.OnSinkError,
	}
}

func stderrSinkError(err error) {
	fmt.Fprintf(os.Stderr, "events: writing the events table failed: %v\n", err)
}

// handler mirrors info-and-above records into the events table.
type handler struct {
	inner      slog.Handler
	store      Store
	clk        clock.Clock
	tableLevel slog.Level
	onErr      func(error)

	// attrs are the attributes fixed by With, kept so the columns the UI
	// filters on survive logger.With(slog.Int64("job_id", id)).
	attrs []slog.Attr

	// grouped is set once WithGroup has run. Attributes inside a group are
	// nested in the JSON and no longer name the columns, so harvesting stops.
	grouped bool
}

var _ slog.Handler = (*handler)(nil)

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

// Handle writes stdout first. plan.md 24: a database failure must never prevent
// the stdout line, so the sink runs afterwards and its error never propagates.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.inner.Handle(ctx, r)

	h.sink(ctx, r)

	if err != nil {
		return fmt.Errorf("write log record: %w", err)
	}

	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	next := h.clone()
	next.inner = h.inner.WithAttrs(attrs)

	if !h.grouped {
		next.attrs = append(next.attrs, attrs...)
	}

	return next
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	next := h.clone()
	next.inner = h.inner.WithGroup(name)
	next.grouped = true

	return next
}

func (h *handler) clone() *handler {
	next := *h
	next.attrs = append([]slog.Attr(nil), h.attrs...)

	return &next
}

// sinkGuard marks a context that is already inside a table write, so a log line
// emitted by the store itself cannot recurse into another insert.
type sinkGuard struct{}

func (h *handler) sink(ctx context.Context, r slog.Record) {
	if r.Level < h.tableLevel {
		return
	}

	if _, inside := ctx.Value(sinkGuard{}).(bool); inside {
		return
	}

	ev := h.event(r)

	// The caller's context is usually a request or a job that is about to be
	// cancelled; the row is still worth writing.
	writeCtx, cancel := context.WithTimeout(
		context.WithValue(context.WithoutCancel(ctx), sinkGuard{}, true), SinkTimeout)
	defer cancel()

	if _, err := h.store.AppendEvent(writeCtx, ev); err != nil {
		h.onErr(err)
	}
}

func (h *handler) event(r slog.Record) domain.Event {
	ev := domain.Event{
		Level:     levelName(r.Level),
		Category:  DefaultCategory,
		Message:   r.Message,
		CreatedAt: r.Time,
	}

	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = h.clk.Now()
	}

	for _, a := range h.attrs {
		harvest(&ev, a)
	}

	if !h.grouped {
		r.Attrs(func(a slog.Attr) bool {
			harvest(&ev, a)

			return true
		})
	}

	return ev
}

// harvest lifts the three attributes the log view filters and links on into
// their own columns. Everything else stays in the JSON line on stdout.
func harvest(ev *domain.Event, a slog.Attr) {
	switch a.Key {
	case "category", "component":
		if s := a.Value.String(); s != "" {
			ev.Category = s
		}
	case "job_id":
		if id := a.Value.Int64(); id != 0 {
			ev.JobID = &id
		}
	case "media_file_id":
		if id := a.Value.Int64(); id != 0 {
			ev.MediaFileID = &id
		}
	}
}

func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// ParseLevel maps the --log-level flag onto a slog level. An unknown value is
// info rather than an error: refusing to start over a typo in a log level is a
// worse outcome than logging slightly more than asked.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redact keeps the Plex token and the *arr API keys out of stdout and out of
// the events table (plan.md 24). It runs on every attribute, nested ones
// included, because the value is a secret wherever it is spelled.
func redact(_ []string, a slog.Attr) slog.Attr {
	if !secretKey(a.Key) {
		return a
	}

	return slog.String(a.Key, domain.MaskedSecret)
}

func secretKey(key string) bool {
	switch strings.ToLower(key) {
	case "token", "plex_token", "auth_token", "authtoken", "x-plex-token",
		"api_key", "apikey", "x-api-key", "secret", "password", "authorization":
		return true
	default:
		return false
	}
}
