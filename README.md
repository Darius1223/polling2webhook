# polling2webhook 🔄

[![CI](https://github.com/Darius1223/polling2webhook/actions/workflows/ci.yml/badge.svg)](https://github.com/Darius1223/polling2webhook/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/Darius1223/polling2webhook/graph/badge.svg)](https://codecov.io/gh/Darius1223/polling2webhook)
[![Go](https://img.shields.io/github/go-mod/go-version/Darius1223/polling2webhook?label=go&logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/Darius1223/polling2webhook)](https://github.com/Darius1223/polling2webhook/releases)

**🌉 A zero-dependency bridge that turns Telegram Bot long polling into webhook delivery.**

Polls the [Telegram Bot API](https://core.telegram.org/bots/api) via `getUpdates` and **POSTs each update as JSON** to your HTTP endpoint — same payload shape as the official Telegram webhook (one `Update` object per request, `Content-Type: application/json`). Supports the `X-Telegram-Bot-Api-Secret-Token` verification header.

**💡 The problem it solves:** your bot handler only understands webhook calls, but you cannot expose a public HTTPS URL to Telegram (local development, corporate network, home server, CI environment, etc.). This tool is a drop-in alternative to tunneling solutions like ngrok, localtunnel, or Cloudflare Tunnel — no external service, no open ports.

```
Telegram servers ──getUpdates──► polling2webhook ──POST /your/handler──► your bot
```

---

## ⚙️ How it works

```
┌─────────────────────────────────────────────────────────────────┐
│                         polling2webhook                          │
│                                                                  │
│  ┌───────────┐  long poll   ┌─────────────┐  HTTP POST (JSON)  │
│  │  Telegram │ ◄──────────► │  getUpdates │ ──────────────────► │ your webhook handler
│  │    API    │  getUpdates  │   loop      │                     │
│  └───────────┘              └─────────────┘                     │
│                              • sorts by update_id               │
│                              • retries with backoff             │
│                              • commits offset after delivery    │
└─────────────────────────────────────────────────────────────────┘
```

- ✅ Calls `getMe` on startup to verify the token before entering the poll loop
- ✅ Sorts updates by `update_id` within each batch — guaranteed in-order delivery
- ✅ Commits the Telegram `offset` **only after** each update is successfully forwarded — no silent drops
- ✅ Retries transient Telegram errors with exponential backoff (up to 30 s)
- ✅ Retries webhook delivery up to 5 times before stopping with an error
- ✅ Logs a redacted config summary on startup (no raw token ever written to logs)
- ✅ Responds to **SIGINT** / **SIGTERM** for graceful shutdown

---

## 🚀 Quick start

### Using `go install` (recommended)

```bash
go install github.com/Darius1223/polling2webhook@latest
polling2webhook -config config.toml
```

### Build from source

```bash
git clone https://github.com/Darius1223/polling2webhook.git
cd polling2webhook
go build -o polling2webhook .
./polling2webhook -config config.toml
```

### 🐳 Docker

```bash
docker run --rm \
  -v "$(pwd)/config.toml:/config/config.toml:ro" \
  ghcr.io/darius1223/polling2webhook
```

Or build locally:

```bash
docker build -t polling2webhook .
docker run --rm -v "$(pwd)/config.toml:/config/config.toml:ro" polling2webhook
```

---

## 🛠️ Configuration

Copy [`config.toml.example`](config.toml.example) to `config.toml` and fill in at least `token`.  
`config.toml` is listed in `.gitignore` so your secrets are never committed by accident.

```toml
# Required
token = "123456:ABC-your-bot-token-from-BotFather"

# Long polling timeout, 1–50 seconds (default 30)
poll_timeout = 30

# Forward every update as JSON to this URL (webhook-compatible)
webhook_url = "http://127.0.0.1:8080/telegram/webhook"

# Optional: sent as X-Telegram-Bot-Api-Secret-Token header (1–256 chars, A-Za-z0-9_-)
webhook_secret = "my-secret"
```

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `token` | ✅ yes | — | Bot token from [@BotFather](https://t.me/BotFather) |
| `poll_timeout` | no | `30` | Seconds to wait for updates (clamped to 1–50) |
| `webhook_url` | no | — | HTTP(S) endpoint that receives each update |
| `webhook_secret` | no | — | Verification header value (`X-Telegram-Bot-Api-Secret-Token`) |

---

## 🏳️ CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.toml` | Path to TOML config file |
| `-log` | `text` | Log format: `text` or `json` |
| `-log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |

---

## 💼 Use cases

- 🖥️ **Local development** — run your webhook bot handler on `localhost` without exposing a port or paying for a tunnel service
- 🏠 **Home server / Raspberry Pi** — no public IP or TLS certificate needed
- 🏢 **Corporate / restricted network** — outbound HTTPS to Telegram is allowed; inbound connections are not
- 🧪 **Testing and CI** — spin up an `httptest.Server`, point `webhook_url` at it, and use `polling2webhook` in integration tests
- 🔌 **Legacy integrations** — adapt a webhook-only framework (e.g. n8n, Make, Zapier) to work with Telegram without changing the framework

---

## ⚖️ Comparison

| Approach | Public URL needed | External service | Works offline |
|---|---|---|---|
| 🟢 **polling2webhook** | No | No | Yes |
| Telegram webhook (native) | Yes (HTTPS) | No | No |
| ngrok / localtunnel | No | Yes | No |
| Cloudflare Tunnel | No | Yes | No |

---

## 🔒 Security

- 🚨 **Never commit your bot token.** `config.toml` is git-ignored by default. If you accidentally committed one, [revoke it via BotFather](https://t.me/BotFather) immediately.
- Use `webhook_secret` so your handler can verify that requests come from this bridge (check the `X-Telegram-Bot-Api-Secret-Token` header).
- In production, prefer injecting secrets via your orchestrator (Kubernetes secrets, Docker secrets, environment variables substituted into config at startup).
- The binary logs a **redacted** config on startup — only `token_len` is recorded, never the raw value.

---

## 🧪 Tests

```bash
go test ./... -race -count=1
```

To see coverage:

```bash
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out
```

---

## 📦 Releases

Pre-built binaries for all major platforms are attached to every [GitHub Release](https://github.com/Darius1223/polling2webhook/releases):

| Platform | Binary |
|---|---|
| 🐧 Linux amd64 | `polling2webhook-linux-amd64` |
| 🐧 Linux arm64 | `polling2webhook-linux-arm64` |
| 🍎 macOS amd64 | `polling2webhook-darwin-amd64` |
| 🍎 macOS arm64 (Apple Silicon) | `polling2webhook-darwin-arm64` |
| 🪟 Windows amd64 | `polling2webhook-windows-amd64.exe` |

To cut a release:

```bash
git tag v1.0.0
git push origin v1.0.0
```

---

## 🤝 Contributing

Bug reports and pull requests are welcome. Please open an issue first for larger changes.

```bash
go vet ./...
go test ./... -race -count=1
```

---

## 📄 License

[MIT](LICENSE) © [Ildar Idiatulin](mailto:dar.290199@mail.ru)
