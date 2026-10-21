package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/web"
	"github.com/yama6a/gokit/httpserver"
	"go.uber.org/zap"
)

// DrainTimeout is how long in-flight requests get after SIGTERM; it must stay under the
// pod's terminationGracePeriodSeconds or the kubelet sends SIGKILL mid-drain.
const DrainTimeout = 20 * time.Second

func (a *app) serve(ctx context.Context) error {
	a.startup(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	background := []struct {
		name string
		run  func(context.Context) error
	}{
		{"queue worker", a.queue.Run},
		{"scan scheduler", a.scheduler.Run},
		{"events pruner", a.pruner.Run},
		{"metrics refresher", a.refresher.Run},
	}

	for _, b := range background {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := b.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				a.logger.Error("background task stopped", zap.String("task", b.name), zap.Error(err))
			}
		}()
	}

	// A failed readiness ping is what drops a pod with an unreachable volume out of
	// the endpoints list.
	err := httpserver.New(httpserver.Options{
		Addr:         a.cfg.listen,
		Ready:        a.db.Reader().PingContext,
		Metrics:      a.metrics.Handler(),
		API:          a.router(),
		DrainTimeout: DrainTimeout,
		Logger:       a.logger,
	}).Run(ctx)

	cancel()
	wg.Wait()
	a.logger.Info("stopped")

	if err != nil {
		return fmt.Errorf("http server: %w", err)
	}

	return nil
}

// startup runs the sweeps of plan.md 19.2 in order: the orphan staging sweep is
// only safe once crash recovery has claimed the staging files still in use.
func (a *app) startup(ctx context.Context) {
	if err := a.queue.Recover(ctx); err != nil {
		a.logger.Error("recovering interrupted jobs failed", zap.Error(err))
	}

	// Cache-first, or every restart burns six ffmpeg invocations (plan.md 10.1).
	caps, err := a.hardware.Capabilities(ctx)
	if err != nil {
		a.logger.Error("reading the hardware capabilities failed", zap.Error(err))

		return
	}

	a.logger.Info("hardware capabilities read",
		zap.String("ffmpeg_version", caps.FfmpegVersion),
		zap.String("device", caps.Device),
		zap.String("encoder", string(caps.Select(false).Encoder)),
		zap.Bool("software_fallback", caps.Select(false).Software))
}

// There is no auth middleware here and there must not be one (plan.md 21).
func (a *app) router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(a.requestLogger)

	r.NotFound(a.notFound)

	return a.api.Router(r)
}

// An unknown /api path is a client error and must not get the HTML shell;
// everything else is a client-side route the SPA resolves.
func (a *app) notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, gen.Error{
			Error:   "not_found",
			Message: "no such endpoint: " + r.Method + " " + r.URL.Path,
		})

		return
	}

	web.Handler().ServeHTTP(w, r)
}

// requestLogger logs at debug, not httpserver.Middleware's info: info and above reach
// the events table, and the UI polls every 10 seconds.
func (a *app) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(wrapped, r)

		a.logger.Debug("request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", wrapped.Status()),
			zap.Duration("took", time.Since(started)))
	})
}

func writeJSON(w http.ResponseWriter, status int, body gen.Error) {
	raw, err := json.Marshal(body)
	if err != nil {
		raw = []byte(`{"error":"internal"}`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
