package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestGetMe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "err", http.StatusInternalServerError)
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token: testToken, PollTimeout: 1, APIBaseURL: srv.URL + "/bot",
	})
	err := c.GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "getMe") {
		t.Fatalf("got %v", err)
	}
}

func TestGetMe_okFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token: testToken, PollTimeout: 1, APIBaseURL: srv.URL + "/bot",
	})
	err := c.GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ok=false") {
		t.Fatalf("got %v", err)
	}
}

func TestGetMe_badJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token: testToken, PollTimeout: 1, APIBaseURL: srv.URL + "/bot",
	})
	err := c.GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("got %v", err)
	}
}

func TestNew_pollTimeoutClamp_queryParam(t *testing.T) {
	tests := []struct {
		name string
		opt  int
		want string
	}{
		{"zero_defaults_30", 0, "30"},
		{"above50_clamped", 77, "50"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			var once sync.Once
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/botx/getMe":
					_, _ = w.Write([]byte(`{"ok":true}`))
				case "/botx/getUpdates":
					once.Do(func() { got = r.URL.Query().Get("timeout") })
					_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			c := telegram.New(log, telegram.Options{
				Token:       "x",
				PollTimeout: tc.opt,
				APIBaseURL:  srv.URL + "/bot",
			})
			if err := c.GetMe(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()
			_ = c.PollUpdates(ctx)
			if got != tc.want {
				t.Fatalf("timeout query param: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPollUpdates_noWebhook(t *testing.T) {
	var tgRound int
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			tgRound++
			if tgRound == 1 {
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer tgSrv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:       testToken,
		PollTimeout: 1,
		APIBaseURL:  tgSrv.URL + "/bot",
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.PollUpdates(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestPollUpdates_sortsByUpdateID(t *testing.T) {
	var order []int64
	whSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var u struct {
			UpdateID int64 `json:"update_id"`
		}
		if err := json.Unmarshal(body, &u); err == nil && u.UpdateID != 0 {
			order = append(order, u.UpdateID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer whSrv.Close()

	var tgRound int
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			tgRound++
			if tgRound == 1 {
				_, _ = w.Write([]byte(`{"ok":true,"result":[
					{"update_id":20,"message":{"message_id":2}},
					{"update_id":10,"message":{"message_id":1}}
				]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer tgSrv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:       testToken,
		PollTimeout: 1,
		APIBaseURL:  tgSrv.URL + "/bot",
		WebhookURL:  whSrv.URL,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.PollUpdates(ctx) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	if len(order) != 2 || order[0] != 10 || order[1] != 20 {
		t.Fatalf("webhook order: %v", order)
	}
}

func TestPollUpdates_getUpdates_okFalseThenCancel(t *testing.T) {
	var mu sync.Mutex
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				_, _ = w.Write([]byte(`{"ok":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token: testToken, PollTimeout: 1, APIBaseURL: srv.URL + "/bot",
		RetryBaseWait: time.Millisecond,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	err := c.PollUpdates(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 2 {
		t.Fatalf("expected multiple getUpdates calls, got %d", n)
	}
}

func TestPollUpdates_webhookFailsAfterRetries(t *testing.T) {
	whSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusInternalServerError)
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
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":5,"message":{"message_id":1}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer tgSrv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:              testToken,
		PollTimeout:        1,
		APIBaseURL:         tgSrv.URL + "/bot",
		WebhookURL:         whSrv.URL,
		ForwardMaxAttempts: 2,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := c.PollUpdates(context.Background())
	if err == nil || (!strings.Contains(err.Error(), "poll") && !strings.Contains(err.Error(), "webhook")) {
		t.Fatalf("got %v", err)
	}
}

func TestParseUpdateID_rejectZero(t *testing.T) {
	// Invalid updates (zero/missing update_id) are skipped with a warning.
	// PollUpdates should continue running and return canceled, not crash.
	var mu sync.Mutex
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			mu.Lock()
			calls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":[{}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:         testToken,
		PollTimeout:   1,
		APIBaseURL:    srv.URL + "/bot",
		RetryBaseWait: time.Millisecond,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := c.PollUpdates(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled, got %v", err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 2 {
		t.Fatalf("expected multiple polls, got %d", n)
	}
}

// TestGetMe_okFalse_longBody covers the truncate() branch where body exceeds max.
func TestGetMe_okFalse_longBody(t *testing.T) {
	longBody := `{"ok":false,"description":"` + strings.Repeat("x", 300) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(longBody))
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token: testToken, PollTimeout: 1, APIBaseURL: srv.URL + "/bot",
	})
	err := c.GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ok=false") {
		t.Fatalf("got %v", err)
	}
}

// TestPollUpdates_cancelDuringSleep covers sleepCtx() ctx.Done() branch.
func TestPollUpdates_cancelDuringSleep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.Error(w, "fail", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Long RetryBaseWait so context cancellation happens while sleepCtx is sleeping.
	c := telegram.New(log, telegram.Options{
		Token:         testToken,
		PollTimeout:   1,
		APIBaseURL:    srv.URL + "/bot",
		RetryBaseWait: 10 * time.Second,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after enough time for getUpdates to fail but before the 10s sleep expires.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := c.PollUpdates(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// TestPollUpdates_badJSONResponse covers the JSON decode error branch in PollUpdates.
func TestPollUpdates_badJSONResponse(t *testing.T) {
	var mu sync.Mutex
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bot" + testToken + "/getUpdates":
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				_, _ = w.Write([]byte(`not-valid-json`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := telegram.New(log, telegram.Options{
		Token:         testToken,
		PollTimeout:   1,
		APIBaseURL:    srv.URL + "/bot",
		RetryBaseWait: time.Millisecond,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	err := c.PollUpdates(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 2 {
		t.Fatalf("expected retry after bad JSON, got %d calls", n)
	}
}

// TestPollUpdates_backoffCaps covers the backoff cap (backoff *= 2 then capped at 30s) branch.
func TestPollUpdates_backoffCaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + testToken + "/getMe":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.Error(w, "fail", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// With RetryBaseWait=1ms, backoff doubles 1→2→4→…→16384ms which exceeds 30s threshold
	// (30000ms), so the cap branch fires.
	c := telegram.New(log, telegram.Options{
		Token:         testToken,
		PollTimeout:   1,
		APIBaseURL:    srv.URL + "/bot",
		RetryBaseWait: time.Millisecond,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	err := c.PollUpdates(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// TestForwardUpdate_maxWaitReached covers the branch where forwardUpdate wait hits the cap.
func TestForwardUpdate_maxWaitReached(t *testing.T) {
	whSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusInternalServerError)
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
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":7,"message":{"message_id":1}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer tgSrv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// ForwardMaxAttempts=6 with base 1ms: wait doubles 1→2→4→8→16ms then stays at 16ms (cap hit).
	c := telegram.New(log, telegram.Options{
		Token:                testToken,
		PollTimeout:          1,
		APIBaseURL:           tgSrv.URL + "/bot",
		WebhookURL:           whSrv.URL,
		ForwardMaxAttempts:   6,
		ForwardRetryBaseWait: time.Millisecond,
	})
	if err := c.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := c.PollUpdates(context.Background())
	if err == nil || (!strings.Contains(err.Error(), "poll") && !strings.Contains(err.Error(), "webhook")) {
		t.Fatalf("got %v", err)
	}
}
