package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

const (
	defaultBaseURL      = "https://api.telegram.org/bot"
	webhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token"
	maxForwardAttempts  = 5
	forwardHTTPTimeout  = 45 * time.Second
)

type Options struct {
	Token       string
	PollTimeout int
	WebhookURL  string
	// WebhookSecret is sent as X-Telegram-Bot-Api-Secret-Token when non-empty.
	WebhookSecret string
	// APIBaseURL is the URL prefix before the bot token (default https://api.telegram.org/bot).
	// For example, in tests point this at httptest.Server URL with a "/bot" suffix. Leave empty in production.
	APIBaseURL string
	// ForwardMaxAttempts is the webhook POST retry count (0 = default 5).
	ForwardMaxAttempts int
	// RetryBaseWait is the initial backoff duration for getUpdates retries (0 = default 1s).
	// Intended for tests only — leave unset in production.
	RetryBaseWait time.Duration
	// ForwardRetryBaseWait is the initial backoff for webhook forward retries (0 = default 1s).
	// Intended for tests only — leave unset in production.
	ForwardRetryBaseWait time.Duration
}

type Client struct {
	log                  *slog.Logger
	httpClient           *http.Client
	forwardClient        *http.Client
	baseURL              string
	token                string
	pollTimeout          int
	webhookURL           string
	webhookSecret        string
	offset               int64
	forwardMax           int
	retryBaseWait        time.Duration
	forwardRetryBaseWait time.Duration
}

func New(log *slog.Logger, opt Options) *Client {
	pollTimeout := opt.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 30
	}
	if pollTimeout > 50 {
		pollTimeout = 50
	}
	base := opt.APIBaseURL
	if base == "" {
		base = defaultBaseURL
	}
	fwdMax := opt.ForwardMaxAttempts
	if fwdMax <= 0 {
		fwdMax = maxForwardAttempts
	}
	retryBase := opt.RetryBaseWait
	if retryBase <= 0 {
		retryBase = time.Second
	}
	fwdRetryBase := opt.ForwardRetryBaseWait
	if fwdRetryBase <= 0 {
		fwdRetryBase = time.Second
	}
	return &Client{
		log: log,
		httpClient: &http.Client{
			Timeout: time.Duration(pollTimeout+15) * time.Second,
		},
		forwardClient: &http.Client{
			Timeout: forwardHTTPTimeout,
		},
		baseURL:       base,
		token:         opt.Token,
		pollTimeout:   pollTimeout,
		webhookURL:    opt.WebhookURL,
		webhookSecret: opt.WebhookSecret,
		forwardMax:           fwdMax,
		retryBaseWait:        retryBase,
		forwardRetryBaseWait: fwdRetryBase,
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (c *Client) buildMethodURL(method string, q url.Values) string {
	u := c.baseURL + c.token + "/" + method
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// get performs GET to Telegram Bot API. Does not log full URLs (token is in path).
func (c *Client) get(ctx context.Context, method string, q url.Values) ([]byte, int, error) {
	fullURL := c.buildMethodURL(method, q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("new request %s: %w", method, err)
	}
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	dur := time.Since(start)
	if err != nil {
		c.log.Warn("telegram request failed", "method", method, "duration", dur, "err", err)
		return nil, 0, fmt.Errorf("do %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.log.Error("read response body", "method", method, "status", resp.StatusCode, "err", err)
		return nil, resp.StatusCode, fmt.Errorf("read body %s: %w", method, err)
	}
	c.log.Debug("telegram response", "method", method, "status", resp.StatusCode, "duration", dur, "bytes", len(body))

	if resp.StatusCode != http.StatusOK {
		c.log.Warn("telegram non-OK HTTP status",
			"method", method,
			"status", resp.StatusCode,
			"body_snip", truncate(string(body), 200),
		)
		return body, resp.StatusCode, fmt.Errorf("%s: %s", method, resp.Status)
	}
	return body, resp.StatusCode, nil
}

type apiOK struct {
	OK bool `json:"ok"`
}

func (c *Client) GetMe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	body, _, err := c.get(ctx, "getMe", nil)
	if err != nil {
		return fmt.Errorf("getMe: %w", err)
	}
	var r apiOK
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("getMe decode: %w", err)
	}
	if !r.OK {
		return fmt.Errorf("getMe: telegram ok=false body=%q", truncate(string(body), 200))
	}
	c.log.Info("telegram bot ok", "op", "getMe")
	return nil
}

type getUpdatesResponse struct {
	OK     bool              `json:"ok"`
	Result []json.RawMessage `json:"result"`
}

type updateItem struct {
	id  int64
	raw json.RawMessage
}

type updateIDField struct {
	UpdateID int64 `json:"update_id"`
}

func parseUpdateID(raw json.RawMessage) (int64, error) {
	var u updateIDField
	if err := json.Unmarshal(raw, &u); err != nil {
		return 0, err
	}
	if u.UpdateID == 0 {
		return 0, fmt.Errorf("missing or zero update_id")
	}
	return u.UpdateID, nil
}

func (c *Client) forwardOnce(ctx context.Context, updateJSON []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(updateJSON))
	if err != nil {
		return fmt.Errorf("webhook new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.webhookSecret != "" {
		req.Header.Set(webhookSecretHeader, c.webhookSecret)
	}

	start := time.Now()
	resp, err := c.forwardClient.Do(req)
	dur := time.Since(start)
	if err != nil {
		return fmt.Errorf("webhook do: %w", err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		c.log.Warn("webhook non-2xx", "status", resp.StatusCode, "duration", dur)
		return fmt.Errorf("webhook: %s", resp.Status)
	}
	c.log.Debug("webhook ok", "status", resp.StatusCode, "duration", dur)
	return nil
}

func (c *Client) forwardUpdate(ctx context.Context, updateJSON []byte, updateID int64) error {
	wait := c.forwardRetryBaseWait
	maxWait := 16 * c.forwardRetryBaseWait
	var lastErr error
	max := c.forwardMax
	for attempt := 1; attempt <= max; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.forwardOnce(ctx, updateJSON)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.log.Warn("webhook forward failed", "update_id", updateID, "attempt", attempt, "err", err)
		if attempt == max {
			return fmt.Errorf("webhook forward update_id=%d after %d attempts: %w", updateID, max, lastErr)
		}
		if !sleepCtx(ctx, wait) {
			return ctx.Err()
		}
		if wait < maxWait {
			wait *= 2
		}
	}
	return lastErr
}

// PollUpdates long-polls getUpdates until ctx is cancelled.
func (c *Client) PollUpdates(ctx context.Context) error {
	backoff := c.retryBaseWait
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		q := url.Values{}
		q.Set("timeout", strconv.Itoa(c.pollTimeout))
		if c.offset > 0 {
			q.Set("offset", strconv.FormatInt(c.offset, 10))
		}

		body, _, err := c.get(ctx, "getUpdates", q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.log.Warn("getUpdates failed, retrying", "err", err, "backoff", backoff)
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			continue
		}
		backoff = time.Second

		var gr getUpdatesResponse
		if err := json.Unmarshal(body, &gr); err != nil {
			c.log.Error("getUpdates JSON decode", "err", err, "body_snip", truncate(string(body), 200))
			if !sleepCtx(ctx, c.retryBaseWait) {
				return ctx.Err()
			}
			continue
		}
		if !gr.OK {
			c.log.Warn("getUpdates ok=false", "body_snip", truncate(string(body), 300))
			if !sleepCtx(ctx, c.retryBaseWait) {
				return ctx.Err()
			}
			continue
		}

		items := make([]updateItem, 0, len(gr.Result))
		for _, raw := range gr.Result {
			id, err := parseUpdateID(raw)
			if err != nil {
				c.log.Warn("getUpdates: skipping invalid update", "err", err, "raw", truncate(string(raw), 200))
				continue
			}
			items = append(items, updateItem{id: id, raw: append(json.RawMessage(nil), raw...)})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })

		if len(items) > 0 {
			c.log.Info("updates batch", "count", len(items))
		}

		for _, item := range items {
			if c.webhookURL != "" {
				if err := c.forwardUpdate(ctx, item.raw, item.id); err != nil {
					return fmt.Errorf("poll: %w", err)
				}
			}
			c.offset = item.id + 1
		}

		if len(items) > 0 {
			c.log.Debug("updates raw batch", "payload", string(body))
		}

	}
}
