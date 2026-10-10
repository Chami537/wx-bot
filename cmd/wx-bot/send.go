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
	"path/filepath"
	"strings"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/bridge"
	"github.com/lawrencewzen/wx-bot/internal/config"
)

func cmdSend(configPath string, args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binding := fs.String("binding", "", "binding name")
	to := fs.String("to", "", "destination wxid")
	text := fs.String("text", "", "plain text (no Markdown); \"-\" reads it from stdin")
	image := fs.String("image", "", "path to an image")
	file := fs.String("file", "", "path to a file")
	video := fs.String("video", "", "path to a video")
	caption := fs.String("caption", "", "optional caption sent as a follow-up text")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: wx-bot send --binding B --to WXID (--text T | --text - | --image P | --file P | --video P) [--caption C]\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nUse --text - with a quoted heredoc so quotes and $ in the reply need no escaping:\n  wx-bot send --binding B --to WXID --text - <<'EOF'\n  ...\n  EOF\n")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *text == "-" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			fmt.Fprintf(os.Stderr, "wx-bot send: read stdin: %v\n", err)
			return 1
		}
		*text = strings.TrimRight(string(b), "\r\n")
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
	// The local API runs in another process with its own working directory.
	for _, p := range []*string{&req.Image, &req.File, &req.Video} {
		if *p != "" {
			abs, err := filepath.Abs(*p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "wx-bot send: %v\n", err)
				return 1
			}
			*p = abs
		}
	}

	timeout := 60 * time.Second
	if req.Text == "" {
		timeout = 10 * time.Minute // media upload, up to 100 MB
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Send directly only when `wx-bot run` is not serving the local API.
	// Once the API has the request, its answer is final: falling back could
	// send the message twice and would hide the real error.
	if reached, err := tryLocalSend(ctx, cfg, req); reached {
		if err != nil {
			fmt.Fprintf(os.Stderr, "wx-bot send: %v\n", err)
			return 1
		}
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

// tryLocalSend sends through the running local API. reached is false only
// when the API is not up, in which case the caller may send directly.
func tryLocalSend(ctx context.Context, cfg *config.Config, req bridge.SendRequest) (reached bool, err error) {
	base := "http://" + cfg.LocalAPI.Listen
	if host, port, err := net.SplitHostPort(cfg.LocalAPI.Listen); err == nil {
		base = "http://" + net.JoinHostPort(host, port)
	}
	// No client timeout: media uploads can be slow; the caller's ctx bounds the send.
	hc := &http.Client{}

	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	hreq, err := http.NewRequestWithContext(hctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false, err
	}
	hresp, err := hc.Do(hreq)
	if err != nil {
		return false, err
	}
	io.Copy(io.Discard, hresp.Body)
	hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("local api healthz %s", hresp.Status)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return true, err
	}
	preq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/send", bytes.NewReader(body))
	if err != nil {
		return true, err
	}
	preq.Header.Set("Content-Type", "application/json")
	if cfg.LocalAPI.Token != "" {
		preq.Header.Set("Authorization", "Bearer "+cfg.LocalAPI.Token)
	}
	presp, err := hc.Do(preq)
	if err != nil {
		// The request may have been sent; do not retry directly.
		return true, fmt.Errorf("local api send (outcome unknown, not retried): %w", err)
	}
	defer presp.Body.Close()
	raw, _ := io.ReadAll(presp.Body)
	if presp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return true, fmt.Errorf("%s (HTTP %d)", e.Error, presp.StatusCode)
		}
		return true, fmt.Errorf("local api send: %s: %s", presp.Status, bytes.TrimSpace(raw))
	}
	return true, nil
}
