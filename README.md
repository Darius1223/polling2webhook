# polling2webhook

Long polling bridge for the [Telegram Bot API](https://core.telegram.org/bots/api): reads updates via `getUpdates` and **POSTs each update as JSON** to your HTTP endpoint, matching the payload shape of the official **webhook** delivery (single `Update` object per request, `Content-Type: application/json`). Optional secret header `X-Telegram-Bot-Api-Secret-Token` is supported.

Use this when your consumer only speaks “webhook” (e.g. local integration) but you do not expose a public HTTPS URL to Telegram.

## Requirements

- Go **1.25+** (see [`go.mod`](go.mod))

## Build and run

```bash
go build -o polling2webhook .
./polling2webhook -config config.toml
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.toml` | Path to TOML config |
| `-log` | `text` | Log encoding: `text` or `json` |
| `-log-level` | `info` | `debug`, `info`, `warn`, `error` |

## Configuration

Copy [`config.toml.example`](config.toml.example) to `config.toml` and set at least `token`. Local `config.toml` is listed in [`.gitignore`](.gitignore) so secrets are not committed by default.

| Key | Required | Description |
|-----|----------|-------------|
| `token` | yes | Bot token from [@BotFather](https://t.me/BotFather) |
| `poll_timeout` | no | Long polling `timeout` for `getUpdates`, seconds 1–50 (default **30**) |
| `webhook_url` | no | If set, each update is POSTed here (webhook-compatible body) |
| `webhook_secret` | no | If set, sent as `X-Telegram-Bot-Api-Secret-Token` |

On startup the process logs a **redacted** summary of the config (no raw token).

## Behavior

- Validates the bot with `getMe` before polling.
- Respects **SIGINT** / **SIGTERM** (graceful stop).
- Retries Telegram and webhook failures with backoff; webhook delivery uses several attempts before failing the process (so updates are not silently dropped).
- Commits Telegram `offset` only after each update is processed (and successfully forwarded, if `webhook_url` is set).

## Security

- Rotate any token that was ever committed to git.
- Use `webhook_secret` so your HTTP handler can verify the caller.
- Prefer secrets from your orchestrator (Kubernetes secrets, etc.) mounted into the container or env-substituted into config.

## Tests

```bash
go test ./... -count=1
```

With the race detector:

```bash
go test ./... -race -count=1
```

## Docker

Build (from repository root):

```bash
docker build -t polling2webhook .
```

Run with a host config file mounted read-only:

```bash
docker run --rm -v "$(pwd)/config.toml:/config/config.toml:ro" polling2webhook
```

The image entrypoint passes `-config /config/config.toml` by default (see [`Dockerfile`](Dockerfile)).

## CI

GitHub Actions workflow [`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs `go vet` and `go test -race`.
