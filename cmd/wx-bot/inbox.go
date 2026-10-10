//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/inbox"
	"github.com/lawrencewzen/wx-bot/internal/state"
)

const inboxUsage = `usage:
  wx-bot inbox count [--binding B]
  wx-bot inbox pull  [--binding B] [--limit N] [--lease 5m] [--wait 0s]
  wx-bot inbox ack   <event_id>...

Only bindings with "delivery": "pull" are served.
count  prints how many events are ready (does not claim; safe for dry runs).
pull   claims events and prints {"events": [...]}; unacked events return after the lease.
ack    marks events handled and prints {"acked": N}.
`

func cmdInbox(configPath string, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, inboxUsage)
		return 2
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("inbox "+sub, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, inboxUsage) }
	binding := fs.String("binding", "", "pull binding (default: all pull bindings)")
	limit := fs.Int("limit", inbox.DefaultPullLimit, "max events to claim")
	lease := fs.Duration("lease", inbox.DefaultLease, "how long claimed events stay hidden before redelivery")
	wait := fs.Duration("wait", 0, "long-poll: wait up to this long for an event")
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: %v\n", err)
		return 1
	}
	scope, err := inbox.Scope(cfg.PullBindings(), *binding)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot inbox: %v\n", err)
		return 1
	}
	box, err := openInbox(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot inbox: %v\n", err)
		return 1
	}
	defer box.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch sub {
	case "count":
		n, err := box.Count(ctx, scope)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wx-bot inbox: %v\n", err)
			return 1
		}
		fmt.Println(n)
		return 0

	case "pull":
		if *limit < 1 || *limit > inbox.MaxPullLimit {
			fmt.Fprintf(os.Stderr, "wx-bot inbox: --limit must be 1..%d\n", inbox.MaxPullLimit)
			return 2
		}
		if *lease < time.Second || *lease > inbox.MaxLease {
			fmt.Fprintf(os.Stderr, "wx-bot inbox: --lease must be 1s..%s\n", inbox.MaxLease)
			return 2
		}
		if *wait < 0 || *wait > inbox.MaxWait {
			fmt.Fprintf(os.Stderr, "wx-bot inbox: --wait must be 0..%s\n", inbox.MaxWait)
			return 2
		}
		events, err := box.ClaimWait(ctx, scope, *limit, *lease, *wait)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wx-bot inbox: %v\n", err)
			return 1
		}
		return printJSON(map[string]any{"events": inbox.Payloads(events)})

	case "ack":
		ids := fs.Args()
		if len(ids) == 0 {
			fmt.Fprint(os.Stderr, inboxUsage)
			return 2
		}
		n, err := box.Ack(ctx, scope, ids)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wx-bot inbox: %v\n", err)
			return 1
		}
		return printJSON(map[string]any{"acked": n})

	default:
		fmt.Fprintf(os.Stderr, "wx-bot inbox: unknown subcommand %q\n", sub)
		fmt.Fprint(os.Stderr, inboxUsage)
		return 2
	}
}

func openInbox(cfg *config.Config) (*inbox.Inbox, error) {
	if err := state.MkdirAll0700(cfg.StateDir); err != nil {
		return nil, err
	}
	return inbox.Open(cfg.InboxPath())
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot inbox: %v\n", err)
		return 1
	}
	return 0
}
