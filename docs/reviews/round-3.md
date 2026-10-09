# Review round 3

## Planner findings

1. **[MAJOR] [internal/config/config.go:223-236](file:///workspace/wx-bot/internal/config/config.go#L223-L236) — `env:NAME` unset/empty handling**
   - **Status**: FIXED
   - **Reason**: [`expandString`](file:///workspace/wx-bot/internal/config/config.go#L223-L236) now checks `os.LookupEnv(name)` and returns `fmt.Errorf("environment variable %s is unset or empty", name)` if unset or empty. [`c.expand()`](file:///workspace/wx-bot/internal/config/config.go#L94-L135) attaches the configuration field path (e.g., `bindings[0].webhook.key`). Error messages only report the field name and variable name without exposing any values or secrets. Regression tests [`TestUnsetEnvVarError`](file:///workspace/wx-bot/internal/config/config_test.go#L115-L132) and [`TestEmptyEnvVarError`](file:///workspace/wx-bot/internal/config/config_test.go#L134-L150) verify this behavior.

2. **[MAJOR] [deploy/systemd/wx-bot.service:12](file:///workspace/wx-bot/deploy/systemd/wx-bot.service#L12), [deploy/launchd/com.lawrencewzen.wx-bot.plist:8-12](file:///workspace/wx-bot/deploy/launchd/com.lawrencewzen.wx-bot.plist#L8-L12), [install.sh:95-99](file:///workspace/wx-bot/install.sh#L95-L99) — Service environment injection**
   - **Status**: FIXED
   - **Reason**: [wx-bot.service](file:///workspace/wx-bot/deploy/systemd/wx-bot.service#L12) now includes `EnvironmentFile=-%h/.config/wx-bot/env`. [com.lawrencewzen.wx-bot.plist](file:///workspace/wx-bot/deploy/launchd/com.lawrencewzen.wx-bot.plist#L8-L12) uses `/bin/sh -c` to execute `set -a; [ -f "$HOME/.config/wx-bot/env" ] && . "$HOME/.config/wx-bot/env"; exec /usr/local/bin/wx-bot run` with correct XML escaping (`&amp;&amp;`). [install.sh](file:///workspace/wx-bot/install.sh#L95-L99) installs [examples/env.example](file:///workspace/wx-bot/examples/env.example) to `~/.config/wx-bot/env` (mode `0600`) without overwriting existing files, and [README.md](file:///workspace/wx-bot/README.md#L91) documents the setup.

3. **[MINOR] [README.md:185-187](file:///workspace/wx-bot/README.md#L185-L187) — 安全 section wording**
   - **Status**: FIXED
   - **Reason**: The security section was updated to clarify that secret keys can be supplied as literals in `config.json`, via `env:VAR`, or in `~/.config/wx-bot/env`, with recommendations to avoid committing secrets to git.

4. **[MINOR] [README.md:148](file:///workspace/wx-bot/README.md#L148) — Grok Bot routine webhook header note**
   - **Status**: FIXED
   - **Reason**: Added explicit documentation noting that Grok Bot routine key headers are unverified on live network, mentioning the default `Authorization: Bearer <key>` behavior and referencing `key_header` and `key_prefix` configuration knobs for customization.

5. **[MINOR] [install.sh:66-71](file:///workspace/wx-bot/install.sh#L66-L71) — Default VERSION from git**
   - **Status**: FIXED
   - **Reason**: [install.sh](file:///workspace/wx-bot/install.sh#L66-L71) checks `git -C "$ROOT" describe --tags --always --dirty` when inside a git worktree and defaults `VERSION` accordingly, falling back to `dev` if git is absent or fails.

---

## New findings

No new findings.

All areas in the round 3 diff were verified:
- **`install.sh`**: Creates `~/.config/wx-bot/env` with mode `0600` from template only if missing; does not overwrite existing configuration or env files; correctly substitutes `${BINDIR}` into systemd unit and launchd plist paths; version extraction is robust under `set -euo pipefail`; fully compatible with Bash 3.2+ (macOS `/bin/bash`) and Linux bash without bash 4+ features.
- **Launchd plist**: The shell wrapper properly escapes XML entities (`&amp;&amp;`), quotes `$HOME/.config/wx-bot/env`, exports variables via `set -a`, and directly `exec`s the target binary to preserve signal handling.
- **Systemd unit**: Uses `EnvironmentFile=-%h/.config/wx-bot/env` with optional prefix `-` and user home specifier `%h`.
- **Config & Secret Redaction**: [`expandString`](file:///workspace/wx-bot/internal/config/config.go#L223-L236) and [`c.expand()`](file:///workspace/wx-bot/internal/config/config.go#L94-L135) error messages report only variable names and config field paths, never secret values.
- **Documentation**: [README.md](file:///workspace/wx-bot/README.md) accurately reflects all configuration fields, service workflows, and security considerations.

---

VERDICT: CLEAN
