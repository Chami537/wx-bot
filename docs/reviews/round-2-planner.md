# Round 2 — planner verification findings (Grok Bot)

Antigravity round 2 verdict was CLEAN; the planner's own install/run smoke test found these:

1. [MAJOR] internal/config/config.go expandString: `env:NAME` with NAME unset silently becomes "" -> webhook is sent without auth. Fix: error at config load when a referenced env var is unset/empty (message names the variable and the field, never a value).
2. [MAJOR] deploy/systemd/wx-bot.service and launchd plist: example config uses env:WXBOT_WEBHOOK_KEY / env:WXBOT_WEBHOOK_SECRET but services have no way to receive them. Fix: systemd `EnvironmentFile=-%h/.config/wx-bot/env`; install.sh creates `~/.config/wx-bot/env` (0600, commented template) if missing; for launchd, wrap ExecStart via `/bin/sh -c 'set -a; [ -f "$HOME/.config/wx-bot/env" ] && . "$HOME/.config/wx-bot/env"; exec <bin> run'` (or equivalent) so the same env file is honoured. Document in README.
3. [MINOR] README 安全 section claims "密钥只允许 env: 或文件" — inaccurate (literal values allowed). Fix wording.
4. [MINOR] README "接到 Grok Bot": the exact header Grok Bot routine webhooks expect for the sender key is unverified; say so and point to key_header/key_prefix to adjust.
5. [MINOR] install.sh builds with VERSION unset -> `wx-bot version` prints "dev". Fix: default VERSION from `git describe --tags --always --dirty` when in a git checkout.
