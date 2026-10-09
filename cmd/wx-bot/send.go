//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
	"github.com/lawrencewzen/wx-bot/internal/config"
)

func cmdSend(configPath string, args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binding := fs.String("binding", "", "binding name")
	to := fs.String("to", "", "destination wxid")
	text := fs.String("text", "", "plain text (no Markdown)")
	image := fs.String("image", "", "path to an image")
	file := fs.String("file", "", "path to a file")
	video := fs.String("video", "", "path to a video")
	caption := fs.String("caption", "", "optional caption sent as a follow-up text")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: wx-bot send --binding B --to WXID (--text T | --image P | --file P | --video P) [--caption C]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot: %v\n", err)
		return 1
	}
	req := bridge.SendRequest{
		Binding: *binding,
		To:      *to,
		Text:    *text,
		Image:   *image,
		File:    *file,
		Video:   *video,
		Caption: *caption,
	}
	if err := req.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot send: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := tryLocalSend(ctx, cfg, req); err == nil {
		fmt.Fprintln(os.Stdout, "sent")
		return 0
	}

	lb, err := bridge.OpenLive(cfg, req.Binding)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot send: %v\n", err)
		return 1
	}
	if err := lb.Sender().Send(ctx, req); err != nil {
		fmt.Fprintf(os.Stderr, "wx-bot send: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "sent")
	return 0
}

func tryLocalSend(ctx context.Context, cfg *config.Config, req bridge.SendRequest) error {
	base := "http://" + cfg.LocalAPI.Listen
	if host, port, err := net.SplitHostPort(cfg.LocalAPI.Listen); err == nil {
		base = "http://" + net.JoinHostPort(host, port)
	}
	hc := &http.Client{Timeout: 15 * time.Second}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return err
	}
	hresp, err := hc.Do(hreq)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, hresp.Body)
	hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		return fmt.Errorf("local api healthz %s", hresp.Status)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	preq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	preq.Header.Set("Content-Type", "application/json")
	if cfg.LocalAPI.Token != "" {
		preq.Header.Set("Authorization", "Bearer "+cfg.LocalAPI.Token)
	}
	presp, err := hc.Do(preq)
	if err != nil {
		return err
	}
	defer presp.Body.Close()
	raw, _ := io.ReadAll(presp.Body)
	if presp.StatusCode >= 300 {
		return fmt.Errorf("local api send: %s: %s", presp.Status, bytes.TrimSpace(raw))
	}
	return nil
}
