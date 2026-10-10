//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/inbox"
)

func cmdStatus(configPath string, args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "usage: wx-bot status\n")
		return 2
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: %v\n", err)
		return 1
	}
	if len(cfg.Bindings) == 0 {
		fmt.Fprintln(os.Stdout, "no bindings configured")
		return 0
	}
	// Read-only peek: do not create the inbox if `run` never has.
	var box *inbox.Inbox
	if _, err := os.Stat(cfg.InboxPath()); err == nil {
		if b, err := inbox.Open(cfg.InboxPath()); err == nil {
			box = b
			defer box.Close()
		}
	}
	for _, st := range bridge.StatusAll(cfg, box) {
		fmt.Fprintf(os.Stdout, "binding: %s\n", st.Name)
		fmt.Fprintf(os.Stdout, "  logged_in: %v\n", st.LoggedIn)
		fmt.Fprintf(os.Stdout, "  expired: %v\n", st.Expired)
		fmt.Fprintf(os.Stdout, "  bot_id: %s\n", st.BotID)
		fmt.Fprintf(os.Stdout, "  cursor: %s\n", boolSet(st.CursorSet))
		fmt.Fprintf(os.Stdout, "  allowlist: %s\n", strings.Join(st.Allowlist, ", "))
		fmt.Fprintf(os.Stdout, "  delivery: %s\n", st.Delivery)
		if st.Delivery == config.DeliveryWebhook {
			fmt.Fprintf(os.Stdout, "  webhook: %s\n", st.WebhookHost)
		}
		if st.Inbox != nil {
			fmt.Fprintf(os.Stdout, "  inbox: %d pending, %d dead\n", st.Inbox.Pending, st.Inbox.Dead)
		}
	}
	return 0
}

func boolSet(v bool) string {
	if v {
		return "set"
	}
	return "unset"
}
