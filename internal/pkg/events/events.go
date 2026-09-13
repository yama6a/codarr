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
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
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

// storeCore mirrors info-and-above entries into the events table.
type storeCore struct {
	store Store
	clk   clock.Clock
	level zapcore.LevelEnabler
	onErr func(error)

	// fields are the ones fixed by With, kept so the columns the UI filters on
	// survive logger.With(zap.Int64("job_id", id)).
	fields []zapcore.Field
}

// NewStoreCore returns the events-table half of the logger. It writes info and above,
// as long as level lets the entry through at all.
func NewStoreCore(st Store, clk clock.Clock, level zapcore.LevelEnabler, onErr func(error)) zapcore.Core {
	return &storeCore{store: st, clk: clk, level: level, onErr: onErr}
}

func (c *storeCore) Enabled(l zapcore.Level) bool {
	return l >= zapcore.InfoLevel && c.level.Enabled(l)
}

func (c *storeCore) With(fields []zapcore.Field) zapcore.Core {
	next := *c
	next.fields = make([]zapcore.Field, 0, len(c.fields)+len(fields))
	next.fields = append(next.fields, c.fields...)
	next.fields = append(next.fields, redactFields(fields)...)

	return &next
}

func (c *storeCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}

	return checked
}

// Write never returns an error: plan.md 24 makes stdout the source of truth, and a
// zapcore error would be written to stderr by zap on top of what onErr already reports.
func (c *storeCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	ev := c.event(entry, fields)

	ctx, cancel := context.WithTimeout(context.Background(), SinkTimeout)
	defer cancel()

	if _, err := c.store.AppendEvent(ctx, ev); err != nil {
		c.onErr(err)
	}

	return nil
}

func (c *storeCore) Sync() error { return nil }

func (c *storeCore) event(entry zapcore.Entry, fields []zapcore.Field) domain.Event {
	ev := domain.Event{
		Level:     entry.Level.String(),
		Category:  DefaultCategory,
		Message:   entry.Message,
		CreatedAt: entry.Time,
	}

	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = c.clk.Now()
	}

	// A zap.Namespace nests everything after it, so the encoded map only carries
	// the fields that are still at the top level and can name a column.
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}

	for _, f := range redactFields(fields) {
		f.AddTo(enc)
	}

	harvest(&ev, enc.Fields)

	return ev
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

// Redacting wraps a core so the Plex token and *arr API keys never reach it
// (plan.md 24), on every field including nested ones, because a secret is a secret
// wherever it appears.
func Redacting(inner zapcore.Core) zapcore.Core {
	return &redactingCore{Core: inner}
}

type redactingCore struct {
	zapcore.Core
}

func (c *redactingCore) With(fields []zapcore.Field) zapcore.Core {
	return &redactingCore{Core: c.Core.With(redactFields(fields))}
}

func (c *redactingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}

	return checked
}

func (c *redactingCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if err := c.Core.Write(entry, redactFields(fields)); err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}

	return nil
}

func redactFields(fields []zapcore.Field) []zapcore.Field {
	out := fields
	copied := false

	for i, f := range fields {
		masked, changed := redactField(f)
		if !changed {
			continue
		}

		if !copied {
			out = append([]zapcore.Field(nil), fields...)
			copied = true
		}

		out[i] = masked
	}

	return out
}

func redactField(f zapcore.Field) (zapcore.Field, bool) {
	if secretKey(f.Key) {
		return zap.String(f.Key, domain.MaskedSecret), true
	}

	if !nests(f.Type) {
		return f, false
	}

	enc := zapcore.NewMapObjectEncoder()
	f.AddTo(enc)

	redacted, changed := redactValue(enc.Fields[f.Key])
	if !changed {
		return f, false
	}

	return zap.Any(f.Key, redacted), true
}

// nests reports whether a field can carry keys of its own, which a secret could hide under.
func nests(t zapcore.FieldType) bool {
	return t == zapcore.ArrayMarshalerType || t == zapcore.ObjectMarshalerType ||
		t == zapcore.InlineMarshalerType || t == zapcore.ReflectType
}

func redactValue(v any) (any, bool) {
	switch val := v.(type) {
	case map[string]any:
		changed := false
		out := make(map[string]any, len(val))

		for k, inner := range val {
			if secretKey(k) {
				out[k] = domain.MaskedSecret
				changed = true

				continue
			}

			redacted, innerChanged := redactValue(inner)
			out[k] = redacted
			changed = changed || innerChanged
		}

		return out, changed
	case []any:
		changed := false
		out := make([]any, len(val))

		for i, inner := range val {
			redacted, innerChanged := redactValue(inner)
			out[i] = redacted
			changed = changed || innerChanged
		}

		return out, changed
	default:
		return v, false
	}
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
