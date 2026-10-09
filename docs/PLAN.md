# wx-bot 实施计划 / Implementation Plan

Planner: Grok Bot (main). Coder: Grok CLI (`grok -m grok-4.6`). Reviewer: Antigravity (`agy`). All agent sessions are driven through Orca terminals.

## 1. Goal
`wx-bot` is a standalone WeChat bridge for personal agent bots, built on Tencent's official
微信 ClawBot / iLink API (`https://ilinkai.weixin.qq.com`). It is extracted from
`lawrencewzen/alice` `internal/wechat/` (read-only reference copy: `/workspace/tmp/alice/internal/wechat/`).
Outbound connections only; never listens on a public interface.

Flow: WeChat user -> iLink long-poll `getupdates` -> wx-bot downloads/decrypts media ->
POST JSON to the binding's webhook (e.g. a Grok Bot routine webhook) -> agent later calls
`wx-bot send` (CLI) or the local HTTP API -> iLink `sendmessage` back to the WeChat user.

## 2. Hard constraints
- Go module `github.com/lawrencewzen/wx-bot`, Go 1.24, **standard library only** except a
  small terminal-QR library (e.g. `github.com/mdp/qrterminal/v3`) if needed. No CGO.
- Platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. **No Windows.**
  Every non-test .go file in package main carries `//go:build linux || darwin`; add a
  `unsupported_windows.go`-style guard so `GOOS=windows go build` fails with a clear message
  Use a file `unsupported.go` with `//go:build !linux && !darwin` that references an undefined identifier with a clear name like `wx_bot_supports_only_linux_and_macos` so the build fails.
- Secrets (bot_token, context_token, webhook key/secret) are never logged; log redacted forms only.
- State files: dir 0700, files 0600, atomic write (tmp + rename).
- Plain-text replies only (strip/avoid Markdown is the agent's job; wx-bot sends text as-is, but
  docs must say "plain text, no Markdown").
- Do not modify anything outside `/workspace/wx-bot`. Do not push. Do not touch alice.

## 3. Layout
```
cmd/wx-bot/main.go            CLI entry: login | run | send | status | version
internal/ilink/               protocol: types.go headers.go client.go login.go crypto.go split.go
internal/config/              YAML-free config: JSON file (config.json), parse + validate + defaults
internal/state/               per-binding state: session.json, context_tokens.json, cursor
internal/bridge/              poller (per binding), webhook dispatcher, allowlist, media download
internal/localapi/            127.0.0.1-only HTTP server for replies
deploy/systemd/wx-bot.service systemd --user unit
deploy/launchd/com.lawrencewzen.wx-bot.plist
examples/config.example.json
install.sh  Makefile  README.md  docs/PLAN.md
```

## 4. Config (JSON, default path `~/.config/wx-bot/config.json`, override `-config`)
```json
{
  "state_dir": "~/.local/state/wx-bot",
  "media_dir": "~/.local/state/wx-bot/media",
  "local_api": { "listen": "127.0.0.1:18790", "token": "" },
  "bindings": [
    {
      "name": "xiaox",
      "webhook": { "url": "https://...", "key": "", "key_header": "Authorization", "key_prefix": "Bearer ", "secret": "" },
      "allow_wxids": [],
      "forward_unallowed": false
    }
  ]
}
```
- `name`: `[a-z0-9_-]{1,32}`, unique. Each binding = one WeChat account = one ClawBot = one agent.
- `webhook.key`: sent as header `key_header: key_prefix+key` (defaults `Authorization` / `Bearer `).
  `webhook.secret` optional: if set, add `X-WxBot-Signature: sha256=<hex HMAC-SHA256(body)>` and
  `X-WxBot-Timestamp`. Any value may be given as `env:VAR_NAME` to read from environment.
- `allow_wxids` empty => default allowlist is the `ilink_user_id` saved at login (the wxid that scanned).
- `local_api.listen` must resolve to a loopback address (127.0.0.1 / ::1 / localhost); reject otherwise.
  If `local_api.token` set, require `Authorization: Bearer <token>`.
- Validation errors are explicit; unknown fields rejected (`DisallowUnknownFields`).

## 5. State (per binding, under `state_dir/<binding>/`)
- `session.json`: `bot_token, base_url, ilink_bot_id, ilink_user_id, created_at` (0600).
- `context_tokens.json`: map wxid -> context_token (persisted on change, 0600).
- `cursor`: last `get_updates_buf`.
- `expired` marker file written when errcode -14 seen; `run` skips expired bindings with a clear log
  line "binding X session expired: run `wx-bot login X`"; `login` clears it.

## 6. Protocol (port from alice, keep behavior identical)
- Headers: `iLink-App-Id: bot`, `iLink-App-ClientVersion: 131335`, random `X-WECHAT-UIN`,
  `AuthorizationType: ilink_bot_token`, `Authorization: Bearer <token>`; channel_version 2.1.7.
- Login: `GET /ilink/bot/get_bot_qrcode?bot_type=3` -> qrcode + qrcode_img_content; poll
  `get_qrcode_status` every 1s; handle `wait|scaned|scaned_but_redirect(redirect_host)|expired|confirmed`;
  refresh QR up to 3 times on expiry. Print QR as terminal QR (encode `qrcode_img_content` URL) plus the URL.
- Base URLs must be overridable for tests (auth base + session base_url).
- `getupdates` long-poll (35s default, honour `longpolling_timeout_ms`, HTTP timeout +10s),
  retry 2s, after 3 consecutive failures back off 30s. Normal timeout = empty result.
- errcode / ret `-14` => mark binding expired, stop that binding's poller only, log ERROR.
- Media: AES-128-ECB + PKCS7, key parse (raw16 / hex32 / base64 of either) exactly as alice
  `parseAESKey`; image `aes_key` hex override; download via `full_url`; upload flow as alice `SendFile`.
- Text split: by runes (alice `splitText`, max 2000 runes per chunk), send chunks in order.
- Item types: text, image, voice (use `voice_item.text` when present = voice-to-text), file, video.

## 7. Webhook payload (POST, `Content-Type: application/json`, 15s timeout, 3 retries w/ backoff on 5xx/network)
```json
{
  "binding": "xiaox",
  "message_id": "…", "from_wxid": "…", "to_bot_id": "…@im.bot",
  "create_time": 1760000000,
  "text": "joined text items",
  "voice_text": "…",
  "media": [ { "type": "image|voice|file|video", "path": "/abs/path", "file_name": "…", "size": 123 } ],
  "media_errors": ["…"],
  "has_context_token": true,
  "reply_hint": "wx-bot send --binding xiaox --to <wxid> --text '...'"
}
```
Never include bot_token / context_token / webhook key in the payload or logs.
Media saved to `media_dir/<binding>/<yyyymmdd>/<msgid>_<sanitized name>` (dir 0700, file 0600).
Unallowed senders: log at INFO with wxid, do not forward (unless `forward_unallowed`), never reply.

## 8. Replies
- CLI: `wx-bot send --binding B --to WXID (--text T | --image P | --file P | --video P) [--caption C]`.
  If `run` is active and local API reachable, CLI may call it; otherwise sends directly using state files.
  Uses cached context_token for WXID; error clearly if none cached ("user must message the bot first").
  Refuse to send to wxids not in the binding's allowlist.
- Local API (only when `run`): `POST /v1/send` JSON `{binding,to,text|image|file|video,caption}`;
  `GET /v1/status`; `GET /healthz`. Bind loopback only.

## 9. CLI
- `wx-bot login <binding>`: QR login, save session, clear expired marker, print allowlisted wxid.
- `wx-bot run`: start pollers for all non-expired logged-in bindings + local API; graceful SIGINT/SIGTERM.
- `wx-bot send …` (above). `wx-bot status`: per binding: logged in?, expired?, bot id (redacted), cursor set?, allowlist, webhook host (no key).
- `wx-bot version`. Global `-config`, `-log-level`. Logs via `log/slog` text to stderr.

## 10. Tests (must pass: `go vet ./...`, `go test ./...`)
- crypto: AES-ECB encrypt/decrypt round trip, PKCS7 edge cases, parseAESKey all 4 forms.
- split: ASCII, CJK, emoji, exact boundaries.
- config: valid parse, `env:` expansion, defaults, duplicate names, bad name, non-loopback listen rejected, unknown field rejected.
- state: perms 0600/0700, atomic write, context token persistence.
- poller: fake iLink server (httptest) delivering text + voice-with-text + image (encrypted, served from fake CDN) ;
  assert webhook receives correct JSON + auth header + HMAC; allowlist blocks stranger; cursor persisted;
  errcode -14 stops only that binding and writes expired marker.
- login: fake server with `scaned_but_redirect` then `confirmed` on redirect host.
- localapi: rejects non-loopback config, token auth, send routes to fake sendmessage with context_token.

## 11. Build / packaging
- Makefile: `build`, `test`, `vet`, `cross` (4 targets into `dist/wx-bot_<os>_<arch>`), `install`, `clean`. `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w -X main.version=…"`.
- install.sh: detect OS/arch (refuse Windows/MINGW/CYGWIN), build from source if Go present else use `dist/`,
  install to `~/.local/bin` (default) or `/usr/local/bin` with `--system`; create config dir with example config
  (0600) if missing; print next steps. Optional `--service` installs systemd user unit (Linux) or launchd plist (macOS).
- README (Chinese): what it is, architecture diagram (text), install on Linux / macOS, config, login, run,
  send, webhook payload, systemd / launchd / no-systemd (tmux / nohup + cron @reboot) instructions,
  Grok Bot webhook example, security notes, no-Windows statement, limits (iLink -14 relogin, untested items).

## 12. Process
1. Grok (grok-4.6) implements everything above in one pass, runs `go vet`, `go test ./...`, `make cross`.
2. Planner verifies. agy reviews `git diff` (round N) and writes `docs/reviews/round-N.md`.
3. Grok fixes findings. Repeat until clean or 3 rounds.
4. Planner creates private repo `lawrencewzen/wx-bot` and pushes.
