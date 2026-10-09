#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Install wx-bot (Linux / macOS only).

Usage: ./install.sh [--system] [--service] [--prefix DIR]

  --system    install to /usr/local/bin (default: ~/.local/bin)
  --service   install systemd --user unit (Linux) or launchd plist (macOS)
  --prefix    install prefix (default ~/.local or /usr/local with --system)
EOF
}

SYSTEM=0
SERVICE=0
PREFIX=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --system) SYSTEM=1; shift ;;
    --service) SERVICE=1; shift ;;
    --prefix) PREFIX="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

uname_s="$(uname -s 2>/dev/null || echo unknown)"
case "$uname_s" in
  MINGW*|MSYS*|CYGWIN*|Windows_NT|Windows*)
    echo "wx-bot does not support Windows (including MINGW/CYGWIN)." >&2
    exit 1
    ;;
  Linux) GOOS=linux ;;
  Darwin) GOOS=darwin ;;
  *)
    echo "unsupported OS: $uname_s (Linux and macOS only)" >&2
    exit 1
    ;;
esac

uname_m="$(uname -m)"
case "$uname_m" in
  x86_64|amd64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *)
    echo "unsupported arch: $uname_m" >&2
    exit 1
    ;;
esac

if [[ -z "$PREFIX" ]]; then
  if [[ "$SYSTEM" -eq 1 ]]; then
    PREFIX=/usr/local
  else
    PREFIX="${HOME}/.local"
  fi
fi
BINDIR="${PREFIX}/bin"

ROOT="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$BINDIR"

if command -v go >/dev/null 2>&1; then
  echo "building from source with $(go version)"
  (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION:-dev}" -o "${BINDIR}/wx-bot" ./cmd/wx-bot)
else
  DIST="${ROOT}/dist/wx-bot_${GOOS}_${GOARCH}"
  if [[ ! -f "$DIST" ]]; then
    echo "Go is not installed and $DIST is missing. Install Go 1.24+ or provide a prebuilt binary." >&2
    exit 1
  fi
  echo "installing prebuilt $DIST"
  install -m 0755 "$DIST" "${BINDIR}/wx-bot"
fi
chmod 0755 "${BINDIR}/wx-bot"

CFGDIR="${HOME}/.config/wx-bot"
mkdir -p "$CFGDIR"
chmod 0700 "$CFGDIR"
CFG="${CFGDIR}/config.json"
if [[ ! -f "$CFG" ]]; then
  install -m 0600 "${ROOT}/examples/config.example.json" "$CFG"
  echo "wrote example config to $CFG (mode 0600); edit webhook.url before running."
fi

if [[ "$SERVICE" -eq 1 ]]; then
  if [[ "$GOOS" == linux ]]; then
    UNITDIR="${HOME}/.config/systemd/user"
    mkdir -p "$UNITDIR"
    UNIT="${UNITDIR}/wx-bot.service"
    sed "s|%h/.local/bin/wx-bot|${BINDIR}/wx-bot|g" "${ROOT}/deploy/systemd/wx-bot.service" > "$UNIT"
    echo "installed $UNIT"
    if command -v systemctl >/dev/null 2>&1; then
      systemctl --user daemon-reload || true
      echo "enable with: systemctl --user enable --now wx-bot"
    fi
  else
    PLIST_SRC="${ROOT}/deploy/launchd/com.lawrencewzen.wx-bot.plist"
    if [[ "$SYSTEM" -eq 1 ]]; then
      PLIST_DST="/Library/LaunchAgents/com.lawrencewzen.wx-bot.plist"
    else
      PLIST_DST="${HOME}/Library/LaunchAgents/com.lawrencewzen.wx-bot.plist"
      mkdir -p "${HOME}/Library/LaunchAgents"
    fi
    sed "s|/usr/local/bin/wx-bot|${BINDIR}/wx-bot|g" "$PLIST_SRC" > "$PLIST_DST"
    echo "installed $PLIST_DST"
    echo "load with: launchctl load $PLIST_DST"
  fi
fi

cat <<EOF

Installed ${BINDIR}/wx-bot

Next steps:
  1. Edit ${CFG}
  2. ${BINDIR}/wx-bot login <binding>
  3. ${BINDIR}/wx-bot run

Put ${BINDIR} on PATH if needed. Replies are plain text (no Markdown).
EOF
