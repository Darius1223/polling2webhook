package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"polling2webhook/internal/config"
	"polling2webhook/internal/telegram"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file")
	logFormat := flag.String("log", "text", "log format: text or json")
	logLevelStr := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	os.Exit(run(*configPath, *logFormat, *logLevelStr))
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (use debug, info, warn, or error)", s)
	}
}

func run(cfgPath, logFormat, logLevelStr string) int {
	lvl, err := parseLogLevel(logLevelStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(logFormat)) {
	case "text":
		h = slog.NewTextHandler(os.Stderr, opts)
	case "json":
		h = slog.NewJSONHandler(os.Stderr, opts)
	default:
		fmt.Fprintf(os.Stderr, "unknown log format %q (use text or json)\n", logFormat)
		return 1
	}
	logger := slog.New(h)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("load config", "err", err)
		return 1
	}

	logArgs := append([]any{"config_path", cfgPath}, cfg.RedactedLogArgs()...)
	logger.Info("config loaded", logArgs...)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := telegram.New(logger, telegram.Options{
		Token:         cfg.Token,
		PollTimeout:   cfg.PollTimeout,
		WebhookURL:    cfg.WebhookURL,
		WebhookSecret: cfg.WebhookSecret,
	})
	if err := client.GetMe(ctx); err != nil {
		logger.Error("getMe", "err", err)
		return 1
	}

	err = client.PollUpdates(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("shutdown", "reason", "signal")
			return 0
		}
		logger.Error("poll updates", "err", err)
		return 1
	}
	return 0
}
