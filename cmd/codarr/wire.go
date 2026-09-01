package main

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/yama6a/codarr/internal/api"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/events"
	"github.com/yama6a/codarr/internal/pkg/fingerprint"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/metrics"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
	"github.com/yama6a/codarr/internal/promote"
)

// Defaults for the settings row on first run. plan.md 21: the schema ships no
// row, and internal/pkg/store deliberately holds no policy, so the defaults are
// stated here, once, where everything else is wired.
var defaultSettings = domain.Settings{ //nolint:gochecknoglobals // first-run defaults, stated once
	TempDir:             "/tmp",
	QSVDevice:           "/dev/dri/renderD128",
	ScanEnabled:         true,
	ScanCron:            "0 4 * * *",
	ScanRateLimitFPS:    50,
	QueuePaused:         false,
	PrioritiseQuickJobs: true,
	FullHashEnabled:     false,
}

// app is everything constructed, held together only so shutdown can reach it.
type app struct {
	cfg        config
	logger     *slog.Logger
	db         *store.DB
	store      store.Store
	queue      *job.Service
	scheduler  *ingest.Scheduler
	pruner     *events.Pruner
	refresher  *metrics.Refresher
	metrics    *metrics.Metrics
	api        *api.Server
	build      api.Build
	policyHash string
	hardware   *hardware.Prober
}

func (a *app) close() {
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			a.logger.Error("closing the database failed", slog.String("error", err.Error()))
		}
	}
}

//nolint:funlen // pure wiring: one long list of constructors with no branching to hide
func build(ctx context.Context, cfg config) (*app, error) {
	level := events.ParseLevel(cfg.logLevel)

	// The store needs a logger and the logger needs the store, so the store is
	// built twice over the same pools: once to open the events sink, once with
	// the real logger behind it.
	bootstrap := events.New(events.Options{Level: level})

	db, err := store.OpenAndMigrate(ctx, cfg.db, bootstrap)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	logger := events.New(events.Options{
		Level: level,
		Store: store.New(db, bootstrap),
	})

	st := store.New(db, logger)

	clk := clock.System()

	defaults := defaultSettings
	defaults.UpdatedAt = clk.Now()

	if err := st.EnsureSettings(ctx, defaults); err != nil {
		return nil, fmt.Errorf("ensure settings: %w", err)
	}

	settings, err := st.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}

	var (
		fs     = fsx.OS()
		fp     = fingerprint.New(fs)
		probe  = ffprobe.New(cfg.ffprobe)
		hw     = hardware.New(hardware.NewCLI(cfg.ffmpeg), st, fs, clk, settings.QSVDevice, settings.TempDir, logger)
		plexes = newPlexProvider(st, logger)
		arrs   = newArrProvider(st, logger)
		mx     = metrics.New()
		info   = buildInfo()
	)

	// One notifier, passed to both promote and job: promotion notifies on the
	// normal path, and the worker notifies for a promotion that completed during
	// a crash and never got to (plan.md 19.2).
	notifier := arr.NewNotifier(plexes, arrs, logger)

	promoter := promote.New(promote.Deps{
		FS:            fs,
		Clock:         clk,
		Prober:        &prober{cli: probe},
		Guard:         plexes,
		Fingerprinter: fp,
		Notifier:      notifier,
		Copier:        promote.NewFSCopier(fs),
		Logger:        logger,
		TempDir:       settings.TempDir,
		Metrics:       mx,
	})

	ingestAnalyzer := ingest.NewAnalyzer(fs, fp, probe, st, clk, logger)
	scanner := ingest.NewScanner(fs, st, ingestAnalyzer, clk, logger)

	queue := job.New(job.Deps{
		Store:         st,
		Prober:        probe,
		Promoter:      promoter,
		FS:            fs,
		Fingerprinter: fp,
		Notifier:      notifier,
		Hardware:      hw,
		Analyzer:      &analyzer{inner: ingestAnalyzer, store: st},
		NewEncoder:    newEncoder(cfg.ffmpeg),
		Clock:         clk,
		Logger:        logger,
		Metrics:       mx,
		Version:       info.Version,
	})

	server := api.New(api.Deps{
		Store:         st,
		DB:            db.Reader(),
		Queue:         queue,
		Analyzer:      ingestAnalyzer,
		Scanner:       scanner,
		Webhooks:      ingest.NewWebhook(st, ingestAnalyzer, fs, logger),
		Hardware:      hw,
		Fingerprinter: fp,
		FS:            fs,
		PlexAuth:      mustPlexAuth(),
		PlexFactory:   plexes.APIClient,
		ArrFactory:    arrs.APIClient,
		Metrics:       mx,
		Clock:         clk,
		Logger:        logger,
		Build:         info,
		FfmpegVersion: ffmpegVersion(ctx, hw, logger),
	})

	return &app{
		cfg:        cfg,
		logger:     logger,
		db:         db,
		store:      st,
		queue:      queue,
		scheduler:  ingest.NewScheduler(st, scanner, clk, logger),
		pruner:     events.NewPruner(st, clk, logger, 0),
		refresher:  metrics.NewRefresher(mx, st, clk, logger, 0, plexProbe(plexes), arrProbe(st, arrs)),
		metrics:    mx,
		api:        server,
		build:      info,
		policyHash: decide.PolicyHash(),
		hardware:   hw,
	}, nil
}

// newEncoder builds one runner per invocation. The runner turns ffmpeg's
// out_time into a percentage against the probed duration (plan.md 14.3), which
// is per file, so it cannot be a singleton.
func newEncoder(bin string) job.NewEncoder {
	return func(duration time.Duration) job.Encoder {
		return ffmpeg.NewRunner(bin, ffmpeg.DefaultGrace, duration)
	}
}

// ffmpegVersion is read once: the binary cannot change under a running process,
// and reporting it costs one invocation rather than one per request.
func ffmpegVersion(ctx context.Context, hw *hardware.Prober, logger *slog.Logger) string {
	version, err := hw.Version(ctx)
	if err != nil {
		logger.WarnContext(ctx, "could not read the ffmpeg version",
			slog.String("error", err.Error()))

		return ""
	}

	return version
}

// mustPlexAuth builds the plex.tv PIN client. Its only failure mode is an
// unusable base URL, and that URL is a constant.
func mustPlexAuth() *plex.Auth {
	auth, err := plex.NewAuth(plex.AuthConfig{})
	if err != nil {
		panic(fmt.Sprintf("plex auth base url is not usable: %v", err))
	}

	return auth
}

func buildInfo() api.Build {
	out := api.Build{Version: "dev", Commit: "unknown", GoVersion: runtime.Version()}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}

	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		out.Version = info.Main.Version
	}

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			out.Commit = s.Value
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				out.BuiltAt = t
			}
		}
	}

	return out
}
