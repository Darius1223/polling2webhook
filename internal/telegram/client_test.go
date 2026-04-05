package telegram_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"polling2webhook/internal/telegram"
)

const testToken = "testtoken"

func TestGetMe_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+testToken+"/getMe" {
			t.Errorf("path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:       testToken,
		PollTimeout: 1,
		APIBaseURL:  srv.URL + "/bot",
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPollUpdates_forwardsWebhookAndReturnsCanceled(t *testing.T) {
	secret := "hook-secret"
	received := make(chan string, 1)

	whSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 500)
			return
		}
		if got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token"); got != secret {
			t.Errorf("secret header %q", got)
		}
		ct := r.Header.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("content-type %q", ct)
		}
		select {
		case received <- string(body):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer whSrv.Close()

	var tgCalls int
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			tgCalls++
			if tgCalls == 1 {
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":42,"message":{"message_id":1}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer tgSrv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:         testToken,
		PollTimeout:   1,
		APIBaseURL:    tgSrv.URL + "/bot",
		WebhookURL:    whSrv.URL,
		WebhookSecret: secret,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.PollUpdates(ctx)
	}()

	select {
	case body := <-received:
		if !strings.Contains(body, "42") || !strings.Contains(body, "update_id") {
			t.Fatalf("unexpected webhook body: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PollUpdates: want canceled got %v", err)
	}
}

func TestParseUpdateID_rejectZero(t *testing.T) {
	// Covered indirectly via invalid JSON in PollUpdates path; ensure zero id rejected
	// by exercising unmarshal-only: use invalid item in batch — client returns error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			_, _ = w.Write([]byte(`{"ok":true,"result":[{}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:       testToken,
		PollTimeout: 1,
		APIBaseURL:  srv.URL + "/bot",
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := c.PollUpdates(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid update") {
		t.Fatalf("expected invalid update error, got %v", err)
	}
}
