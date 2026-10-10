#!/bin/sh
# Muse hook detector for wx-bot pull delivery.
#
# It only counts ready events (no claim, no side effects), so dry runs are
# safe. Events being handled are leased and not counted, so an agent that is
# already working is not woken again; unacked events come back after the lease.
#
# Wrap the final decision in the Muse hook script protocol:
#   count > 0  -> wake  (wake text: "微信有 N 条新消息，按 wx-bot 指令处理")
#   count = 0  -> silent
#   error      -> silent, and report the error text
set -eu
WXBOT="${WXBOT:-$HOME/.local/bin/wx-bot}"
BINDING="${WXBOT_BINDING:-muse}"

if ! count="$("$WXBOT" inbox count --binding "$BINDING" 2>&1)"; then
	echo "error: $count"
	exit 0
fi
if [ "$count" -gt 0 ]; then
	echo "wake: 微信有 $count 条新消息"
else
	echo "silent"
fi
