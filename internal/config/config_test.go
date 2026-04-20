package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_minimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	content := `token = "my-token"`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "my-token" {
		t.Fatalf("token: got %q", cfg.Token)
	}
	if cfg.PollTimeout != 30 {
		t.Fatalf("poll_timeout default: got %d", cfg.PollTimeout)
	}
}

func TestLoad_emptyPath_usesDefaultNameInError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, err := Load("")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "config.toml") {
		t.Fatalf("error should mention default file: %v", err)
	}
}

func TestLoad_missingToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(path, []byte(`poll_timeout = 10`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("error: %v", err)
	}
}

func TestLoad_invalidWebhookURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	content := `token = "x"
webhook_url = "ftp://example.com/hook"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "webhook_url") {
		t.Fatalf("error: %v", err)
	}
}

func TestLoad_webhookURLEmptyHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	// "https:///path" parses successfully (scheme=https) but has an empty host.
	content := `token = "x"
webhook_url = "https:///webhook"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty host")
	}
	if !strings.Contains(err.Error(), "webhook_url") {
		t.Fatalf("error: %v", err)
	}
}

func TestLoad_pollTimeoutClamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`token = "x"
poll_timeout = 0`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollTimeout != 30 {
		t.Fatalf("0 -> 30: got %d", cfg.PollTimeout)
	}

	write(`token = "x"
poll_timeout = 99`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollTimeout != 50 {
		t.Fatalf(">50 -> 50: got %d", cfg.PollTimeout)
	}
}

func TestLoad_invalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte(`token = [`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error: %v", err)
	}
}

func TestLoad_validWebhookURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	content := `token = "t"
webhook_url = "https://example.com/hook"
poll_timeout = 15
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookURL != "https://example.com/hook" {
		t.Fatalf("webhook_url: %q", cfg.WebhookURL)
	}
	if cfg.PollTimeout != 15 {
		t.Fatalf("poll_timeout: %d", cfg.PollTimeout)
	}
}

func TestLoad_webhookSecretTrimmed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	content := `token = "t"
webhook_secret = "  abc123  "
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookSecret != "abc123" {
		t.Fatalf("secret: %q", cfg.WebhookSecret)
	}
}

func TestLoad_invalidWebhookSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	content := `token = "t"
webhook_secret = "invalid secret with spaces"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid webhook_secret")
	}
	if !strings.Contains(err.Error(), "webhook_secret") {
		t.Fatalf("error: %v", err)
	}
}

func TestRedactedLogArgs_noRawToken(t *testing.T) {
	secret := "super-secret-token-value-12345"
	cfg := Config{
		Token:       secret,
		PollTimeout: 25,
		WebhookURL:  "https://example.com/hook",
	}
	args := cfg.RedactedLogArgs()
	var joined strings.Builder
	for _, a := range args {
		joined.WriteString(fmt.Sprint(a))
	}
	if strings.Contains(joined.String(), secret) {
		t.Fatalf("log args leaked token: %#v", args)
	}
	var tokenLen int
	for i := 0; i < len(args)-1; i += 2 {
		if args[i] == "token_len" {
			tokenLen = args[i+1].(int)
			break
		}
	}
	if tokenLen != len(secret) {
		t.Fatalf("token_len: got %d want %d", tokenLen, len(secret))
	}
}
