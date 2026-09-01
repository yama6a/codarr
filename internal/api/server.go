package api

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/clock"
)

// DefaultPageSize is the library and job page size when a request names none.
const DefaultPageSize = 50

// MaxPageSize bounds what a request can ask for, so one call cannot pull a
// 40,000-row library into memory.
const MaxPageSize = 500

// DefaultEventLimit and MaxEventLimit bound the log view's cursor page.
const (
	DefaultEventLimit = 200
	MaxEventLimit     = 1000
)

// Server implements the generated StrictServerInterface.
type Server struct {
	store    Store
	db       Pinger
	queue    Queue
	analyzer Analyzer
	scanner  Scanner
	webhooks Webhooks
	hardware Hardware
	fp       Fingerprinter
	fs       FS
	plexAuth PlexAuth
	plex     PlexFactory
	arr      ArrFactory
	metrics  Metrics
	clk      clock.Clock
	log      *slog.Logger
	build    Build
	ffmpeg   string

	// compat caches the compatibility breakdown of plan.md 18.1. Deriving the
	// per-reason counts means reading every non-skip plan, and the dashboard
	// polls every 10 seconds (18.6), so the answer is shared across polls.
	compat compatCache
}

var _ gen.StrictServerInterface = (*Server)(nil)

// New returns the API server.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}

	if d.Clock == nil {
		d.Clock = clock.System()
	}

	return &Server{
		store:    d.Store,
		db:       d.DB,
		queue:    d.Queue,
		analyzer: d.Analyzer,
		scanner:  d.Scanner,
		webhooks: d.Webhooks,
		hardware: d.Hardware,
		fp:       d.Fingerprinter,
		fs:       d.FS,
		plexAuth: d.PlexAuth,
		plex:     d.PlexFactory,
		arr:      d.ArrFactory,
		metrics:  d.Metrics,
		clk:      d.Clock,
		log:      d.Logger.With(slog.String("component", "api")),
		build:    d.Build,
		ffmpeg:   d.FfmpegVersion,
	}
}

// Router mounts the generated routes plus the served spec on r. There is no
// authentication middleware and there must not be one: plan.md 21 secures
// access outside the process.
func (s *Server) Router(r chi.Router) http.Handler {
	r.Get("/api/openapi.json", s.serveSpec)

	return gen.HandlerFromMux(gen.NewStrictHandler(s, nil), r)
}

// serveSpec hands out the spec the client types are generated from, so the UI
// and any operator can read what this binary actually serves.
func (s *Server) serveSpec(w http.ResponseWriter, r *http.Request) {
	spec, err := gen.GetSpec()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "spec_unavailable", err.Error())

		return
	}

	body, err := spec.MarshalJSON()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "spec_unavailable", err.Error())

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// compatCache is the memoised compatibility summary.
type compatCache struct {
	mu       sync.Mutex
	value    gen.CompatibilitySummary
	computed time.Time
}

func page(pageNo, size *int) (limit, offset, resolvedPage, resolvedSize int) {
	resolvedSize = DefaultPageSize
	if size != nil && *size > 0 {
		resolvedSize = min(*size, MaxPageSize)
	}

	resolvedPage = 1
	if pageNo != nil && *pageNo > 0 {
		resolvedPage = *pageNo
	}

	return resolvedSize, (resolvedPage - 1) * resolvedSize, resolvedPage, resolvedSize
}
