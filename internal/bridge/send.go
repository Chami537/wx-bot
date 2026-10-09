package bridge

import (
	"context"
	"fmt"

	"github.com/lawrencewzen/wx-bot/internal/ilink"
)

// SendRequest is an outbound message (CLI or local API).
type SendRequest struct {
	Binding string `json:"binding"`
	To      string `json:"to"`
	Text    string `json:"text"`
	Image   string `json:"image"`
	File    string `json:"file"`
	Video   string `json:"video"`
	Caption string `json:"caption"`
}

// Validate checks that exactly one content field is set and required fields exist.
func (r SendRequest) Validate() error {
	if r.Binding == "" {
		return fmt.Errorf("binding is required")
	}
	if r.To == "" {
		return fmt.Errorf("to (wxid) is required")
	}
	n := 0
	if r.Text != "" {
		n++
	}
	if r.Image != "" {
		n++
	}
	if r.File != "" {
		n++
	}
	if r.Video != "" {
		n++
	}
	if n == 0 {
		return fmt.Errorf("one of text, image, file, video is required")
	}
	if n > 1 {
		return fmt.Errorf("text, image, file, video are mutually exclusive")
	}
	return nil
}

// Sender delivers a SendRequest using a live iLink client and allowlist.
type Sender struct {
	Client *ilink.Client
	Allow  Allowlist
}

// Send checks the allowlist and cached context_token, then delivers.
func (s Sender) Send(ctx context.Context, req SendRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if !s.Allow.Contains(req.To) {
		return fmt.Errorf("wxid %s is not in the allowlist for binding %s", req.To, req.Binding)
	}
	if s.Client.ContextToken(req.To) == "" {
		return fmt.Errorf("no context_token for %s: user must message the bot first", req.To)
	}
	switch {
	case req.Text != "":
		return s.Client.SendText(ctx, req.To, req.Text)
	case req.Image != "":
		return s.Client.SendImage(ctx, req.To, req.Image, req.Caption)
	case req.Video != "":
		return s.Client.SendVideo(ctx, req.To, req.Video, req.Caption)
	default:
		return s.Client.SendFile(ctx, req.To, req.File, req.Caption)
	}
}
