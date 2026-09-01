package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/web"
)

// DrainTimeout is how long in-flight requests get after SIGTERM. It must stay
// under the pod's terminationGracePeriodSeconds, which bolan sets to 30; 20
// leaves the kubelet ten seconds of headroom before it sends SIGKILL.
const DrainTimeout = 20 * time.Second

// ReadHeaderTimeout bounds a slow-header client. Everything else is deliberately
// unbounded: a webhook body is small, but a scan triggered by the UI answers
// 202 immediately and the *arr rescans can be slow.
const ReadHeaderTimeout = 10 * time.Second

// serve starts every long-running goroutine, runs the startup sweeps and blocks
// until ctx is cancelled, then drains.
func (a *app) serve(ctx context.Context) error {
	a.startup(ctx)

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
				a.logger.Error("background task stopped",
					slog.String("task", b.name), slog.String("error", err.Error()))
			}
		}()
	}

	server := &http.Server{
		Addr:              a.cfg.listen,
		Handler:           a.handler(),
		ReadHeaderTimeout: ReadHeaderTimeout,
	}

	errCh := make(chan error, 1)

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen on %s: %w", a.cfg.listen, err)

			return
		}

		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	a.logger.Info("shutting down", slog.Duration("drain_timeout", DrainTimeout))

	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DrainTimeout)
	defer cancel()

	if err := server.Shutdown(drainCtx); err != nil {
		a.logger.Error("draining connections failed", slog.String("error", err.Error()))
	}

	wg.Wait()
	a.logger.Info("stopped")

	return <-errCh
}

// startup runs the sweeps of plan.md 19.2 in the order they have to happen:
// crash recovery first, which decides what every interrupted job actually did
// and claims the staging files still in use; then the orphan staging sweep,
// which deletes what nothing claims; then the hardware capability read.
//
// Recovery owns the first two, because the orphan sweep is only safe once the
// claims are known.
func (a *app) startup(ctx context.Context) {
	if err := a.queue.Recover(ctx); err != nil {
		a.logger.ErrorContext(ctx, "recovering interrupted jobs failed", slog.String("error", err.Error()))
	}

	// Cache-first: every restart would otherwise burn six ffmpeg invocations
	// for an answer already in SQLite (plan.md 10.1).
	caps, err := a.hardware.Capabilities(ctx)
	if err != nil {
		a.logger.ErrorContext(ctx, "reading the hardware capabilities failed",
			slog.String("error", err.Error()))

		return
	}

	a.logger.InfoContext(ctx, "hardware capabilities read",
		slog.String("ffmpeg_version", caps.FfmpegVersion),
		slog.String("device", caps.Device),
		slog.String("encoder", string(caps.Select(false).Encoder)),
		slog.Bool("software_fallback", caps.Select(false).Software))
}

// handler is the whole HTTP surface. /healthz, /readyz and /metrics sit on a
// plain mux above the chi router so a kubelet probe and a Prometheus scrape skip
// every middleware; everything else goes through chi.
func (a *app) handler() http.Handler {
	root := http.NewServeMux()

	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, probeResult{Status: "ok"})
	})

	root.HandleFunc("GET /readyz", a.readyz)
	root.Handle("GET /metrics", a.metrics.Handler())
	root.Handle("/", a.router())

	return root
}

// readyz is 503 while the database does not answer a ping, which is what makes
// a pod with an unreachable volume drop out of the endpoints list.
func (a *app) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := a.db.Reader().PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, probeResult{Status: "unavailable", Error: err.Error()})

		return
	}

	writeJSON(w, http.StatusOK, probeResult{Status: "ok"})
}

// router is the application surface: the generated API routes, and the embedded
// SPA for everything that is not one. There is no auth middleware and there must
// not be one (plan.md 21).
func (a *app) router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(a.requestLogger)

	r.NotFound(a.notFound)

	return a.api.Router(r)
}

// notFound splits the two halves of the surface: an unknown /api path is a
// client error and must not be answered with an HTML shell, everything else is a
// client-side route the SPA resolves.
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

func (a *app) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(wrapped, r)

		a.logger.DebugContext(r.Context(), "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", wrapped.Status()),
			slog.Duration("took", time.Since(started)))
	})
}

// probeResult is the body of /healthz and /readyz. It is a named type rather
// than a map so the encode cannot fail on an unserialisable value.
type probeResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func writeJSON[T probeResult | gen.Error](w http.ResponseWriter, status int, body T) {
	raw, err := json.Marshal(body)
	if err != nil {
		raw = []byte(`{"status":"unavailable"}`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
