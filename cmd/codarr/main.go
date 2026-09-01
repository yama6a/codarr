// Command codarr runs the API, the SPA, the queue worker and the scan schedule in one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Bootstrap config is flags and environment only; everything else lives in SQLite (plan.md 21).
const (
	defaultDB       = "/data/codarr.db"
	defaultListen   = ":8080"
	defaultLogLevel = "info"
	defaultFfmpeg   = "ffmpeg"
	defaultFfprobe  = "ffprobe"
)

type config struct {
	db       string
	listen   string
	logLevel string
	ffmpeg   string
	ffprobe  string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "codarr: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	app, err := build(ctx, cfg)
	if err != nil {
		return err
	}

	defer app.close()

	app.logger.Info("starting codarr",
		slog.String("listen", cfg.listen),
		slog.String("db", cfg.db),
		slog.String("version", app.build.Version),
		slog.String("commit", app.build.Commit),
		slog.String("policy_hash", app.policyHash))

	if err := app.serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// parseFlags reads the five bootstrap values of plan.md 21, stdlib flag only.
func parseFlags() config {
	cfg := config{}

	flag.StringVar(&cfg.db, "db", envOr("CODARR_DB", defaultDB), "path to the SQLite database")
	flag.StringVar(&cfg.listen, "listen", envOr("CODARR_LISTEN", defaultListen), "address to listen on")
	flag.StringVar(&cfg.logLevel, "log-level", envOr("CODARR_LOG_LEVEL", defaultLogLevel),
		"log level: debug, info, warn or error")
	flag.StringVar(&cfg.ffmpeg, "ffmpeg", envOr("CODARR_FFMPEG", defaultFfmpeg), "path to the ffmpeg binary")
	flag.StringVar(&cfg.ffprobe, "ffprobe", envOr("CODARR_FFPROBE", defaultFfprobe), "path to the ffprobe binary")
	flag.Parse()

	return cfg
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}

	return fallback
}
