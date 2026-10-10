//go:build linux || darwin

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/lawrencewzen/wx-bot/internal/config"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("wx-bot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "path to config.json (default ~/.config/wx-bot/config.json)")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn, error")
	fs.Usage = func() { usage(fs) }
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	args := fs.Args()
	if len(args) == 0 {
		usage(fs)
		os.Exit(2)
	}

	level, err := parseLevel(*logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: %v\n", err)
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cmd := args[0]
	rest := args[1:]
	if cmd == "version" {
		fmt.Printf("wx-bot %s\n", version)
		return
	}

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}

	var exit int
	switch cmd {
	case "login":
		exit = cmdLogin(path, rest)
	case "run":
		exit = cmdRun(path, rest)
	case "send":
		exit = cmdSend(path, rest)
	case "status":
		exit = cmdStatus(path, rest)
	case "inbox":
		exit = cmdInbox(path, rest)
	case "help", "-h", "--help":
		usage(fs)
	default:
		fmt.Fprintf(os.Stderr, "wx-bot: unknown command %q\n", cmd)
		usage(fs)
		exit = 2
	}
	os.Exit(exit)
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log-level %q", s)
	}
}

func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	return cfg, nil
}

func usage(fs *flag.FlagSet) {
	fmt.Fprintf(os.Stderr, `wx-bot — WeChat iLink bridge for personal agent bots

Usage:
  wx-bot [flags] <command> [args]

Commands:
  login <binding>   Scan QR to log in a binding
  run               Poll iLink, queue messages, deliver to webhooks
  send              Send a message (see wx-bot send -h)
  inbox             Claim / ack queued events for pull bindings (see wx-bot inbox)
  status            Show per-binding login / delivery / inbox state
  version           Print version

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Replies are plain text. Do not send Markdown; the agent should strip it.

Platforms: Linux and macOS only.
`)
}
