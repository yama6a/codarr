// Package events is Codarr's logging: zap JSON to stdout, teed so info and above
// also reach the events table (plan.md 24).
//
// Stdout is the source of truth, so the table core never returns an error and never
// stops the console line being written.
package events

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/gokit/zapx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// SinkTimeout bounds one events-table insert, so a goroutine that was only trying to
// log does not block waiting for the single write connection.
const SinkTimeout = 5 * time.Second

// DefaultCategory is used for an entry carrying no component or category field.
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

	// Level is the floor for stdout. The events table additionally never sees
	// anything below info.
	Level zapcore.Level

	// Store is the events table sink. A nil Store logs to stdout only.
	Store Store

	Clock clock.Clock

	// OnSinkError sees every failed table insert. The default writes one line
	// to stderr, which cannot recurse back into this logger.
	OnSinkError func(error)
}

// New returns the logger the whole binary uses.
func New(o Options) *zap.Logger {
	if o.Out == nil {
		o.Out = os.Stdout
	}

	if o.Clock == nil {
		o.Clock = clock.System()
	}

	if o.OnSinkError == nil {
		o.OnSinkError = stderrSinkError
	}

	level := zap.NewAtomicLevelAt(o.Level)

	console := Redacting(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(o.Out),
		level,
	))

	if o.Store == nil {
		return zap.New(console)
	}

	return zap.New(zapcore.NewTee(console, NewStoreCore(o.Store, o.Clock, level, o.OnSinkError)))
}

func stderrSinkError(err error) {
	fmt.Fprintf(os.Stderr, "events: writing the events table failed: %v\n", err)
}

// NewStoreCore returns the events-table half of the logger. It writes info and above,
// as long as level lets the entry through at all.
func NewStoreCore(st Store, clk clock.Clock, level zapcore.LevelEnabler, onErr func(error)) zapcore.Core {
	infoAndAbove := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
		return l >= zapcore.InfoLevel && level.Enabled(l)
	})

	return Redacting(zapx.SinkCore(tableSink{store: st, clk: clk}, infoAndAbove,
		zapx.WithTimeout(SinkTimeout), zapx.OnError(onErr)))
}

// Redacting wraps a core so the Plex token and *arr API keys never reach it (plan.md 24).
func Redacting(inner zapcore.Core) zapcore.Core {
	return zapx.Redacting(inner, append(zapx.DefaultRedactKeys(),
		"plex_token", "x-plex-token", "x-api-key", "apikey", "auth_token", "authtoken")...)
}

type tableSink struct {
	store Store
	clk   clock.Clock
}

func (s tableSink) Write(ctx context.Context, e zapx.Entry) error {
	ev := domain.Event{
		Level:     e.Level.String(),
		Category:  DefaultCategory,
		Message:   e.Message,
		CreatedAt: e.At,
	}

	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = s.clk.Now()
	}

	// A zap.Namespace nests everything after it, so the encoded map only carries
	// the fields that are still at the top level and can name a column.
	harvest(&ev, e.Fields)

	if _, err := s.store.AppendEvent(ctx, ev); err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	return nil
}

// The three fields the log view filters and links on get their own columns.
func harvest(ev *domain.Event, fields map[string]any) {
	if s, ok := fields["category"].(string); ok && s != "" {
		ev.Category = s
	}

	if s, ok := fields["component"].(string); ok && s != "" {
		ev.Category = s
	}

	if id, ok := int64Field(fields["job_id"]); ok {
		ev.JobID = &id
	}

	if id, ok := int64Field(fields["media_file_id"]); ok {
		ev.MediaFileID = &id
	}
}

func int64Field(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, n != 0
	case int:
		return int64(n), n != 0
	default:
		return 0, false
	}
}
