package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"polling2webhook/internal/config"
	"polling2webhook/internal/telegram"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{" info ", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"fatal", slog.Level(0), true},
		{"", slog.Level(0), true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseLogLevel(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err: %v", err)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("level: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRun_badLogLevel(t *testing.T) {
	if code := run(context.Background(), filepath.Join(t.TempDir(), "x.toml"), "text", "invalid"); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
}

func TestRun_badLogFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(path, []byte(`token = "x"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), path, "xml", "info"); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
}

func TestRun_configNotFound(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.toml")
	if code := run(context.Background(), p, "text", "info"); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
}

func TestRun_successCancel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(cfgPath, []byte(`token = "tok"`), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + "tok" + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + "tok" + "/getUpdates":
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	orig := newTelegramClient
	newTelegramClient = func(log *slog.Logger, cfg config.Config) *telegram.Client {
		return telegram.New(log, telegram.Options{
			Token:       cfg.Token,
			PollTimeout: 1,
			APIBaseURL:  srv.URL + "/bot",
		})
	}
	defer func() { newTelegramClient = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	if code := run(ctx, cfgPath, "json", "debug"); code != 0 {
		t.Fatalf("code=%d want 0 (canceled)", code)
	}
}

func TestRun_getMeError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(cfgPath, []byte(`token = "tok"`), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	orig := newTelegramClient
	newTelegramClient = func(log *slog.Logger, cfg config.Config) *telegram.Client {
		return telegram.New(log, telegram.Options{
			Token:       cfg.Token,
			PollTimeout: 1,
			APIBaseURL:  srv.URL + "/bot",
		})
	}
	defer func() { newTelegramClient = orig }()

	if code := run(context.Background(), cfgPath, "text", "error"); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
}

func TestDefaultNewTelegramClient(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{Token: "abc", PollTimeout: 10}
	c := defaultNewTelegramClient(log, cfg)
	if c == nil {
		t.Fatal("nil client")
	}
}
