package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Token         string `toml:"token"`
	PollTimeout   int    `toml:"poll_timeout"`
	WebhookURL    string `toml:"webhook_url"`
	WebhookSecret string `toml:"webhook_secret"`
}

func Load(path string) (Config, error) {
	if path == "" {
		path = "config.toml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("parse config %q: token is required", path)
	}
	if cfg.PollTimeout == 0 {
		cfg.PollTimeout = 30
	}
	if cfg.PollTimeout > 50 {
		cfg.PollTimeout = 50
	}

	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	cfg.WebhookSecret = strings.TrimSpace(cfg.WebhookSecret)

	if cfg.WebhookURL != "" {
		u, err := url.ParseRequestURI(cfg.WebhookURL)
		if err != nil {
			return Config{}, fmt.Errorf("parse config %q: invalid webhook_url: %w", path, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return Config{}, fmt.Errorf("parse config %q: webhook_url must use http or https scheme", path)
		}
		if u.Host == "" {
			return Config{}, fmt.Errorf("parse config %q: webhook_url must include host", path)
		}
	}

	return cfg, nil
}

// RedactedLogArgs returns key-value pairs for slog.Info without leaking secrets.
func (c Config) RedactedLogArgs() []any {
	return []any{
		"poll_timeout", c.PollTimeout,
		"webhook_url", c.WebhookURL,
		"webhook_forward", c.WebhookURL != "",
		"webhook_secret_set", c.WebhookSecret != "",
		"token_len", len(c.Token),
	}
}
