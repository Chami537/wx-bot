package bridge

import (
	"context"
	"sort"
	"unicode/utf8"

	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/inbox"
	"github.com/lawrencewzen/wx-bot/internal/state"
)

// BindingStatus is the redacted per-binding view for `wx-bot status` and GET /v1/status.
type BindingStatus struct {
	Name        string   `json:"name"`
	LoggedIn    bool     `json:"logged_in"`
	Expired     bool     `json:"expired"`
	BotID       string   `json:"bot_id"`
	CursorSet   bool     `json:"cursor_set"`
	Allowlist   []string `json:"allowlist"`
	Delivery    string   `json:"delivery"`
	WebhookHost string   `json:"webhook_host"`
	// Inbox is nil when the inbox could not be read.
	Inbox *inbox.Counts `json:"inbox,omitempty"`
}

// StatusOf computes status for one configured binding from disk.
// box may be nil.
func StatusOf(cfg *config.Config, b config.Binding, box *inbox.Inbox) BindingStatus {
	st := BindingStatus{
		Name:        b.Name,
		Delivery:    b.Delivery,
		WebhookHost: Host(b.Webhook.URL),
	}
	if box != nil {
		if c, err := box.Stats(context.Background(), b.Name); err == nil {
			st.Inbox = &c
		}
	}
	store, err := state.Open(cfg.StateDir, b.Name)
	if err != nil {
		st.Allowlist = NewAllowlist(b.AllowWxids, "").IDs()
		sort.Strings(st.Allowlist)
		return st
	}
	st.Expired = store.IsExpired()
	st.CursorSet = store.HasCursor()
	loginUser := ""
	if sess, err := store.LoadSession(); err == nil {
		st.LoggedIn = true
		st.BotID = RedactBotID(sess.BotID)
		loginUser = sess.UserID
	}
	st.Allowlist = NewAllowlist(b.AllowWxids, loginUser).IDs()
	sort.Strings(st.Allowlist)
	return st
}

// StatusAll returns status for every configured binding.
func StatusAll(cfg *config.Config, box *inbox.Inbox) []BindingStatus {
	out := make([]BindingStatus, 0, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		out = append(out, StatusOf(cfg, b, box))
	}
	return out
}

// RedactBotID keeps the first 4 runes of a bot id.
func RedactBotID(id string) string {
	if id == "" {
		return ""
	}
	n := utf8.RuneCountInString(id)
	if n <= 4 {
		return id[:len(id)] + "…"
	}
	i := 0
	for range 4 {
		_, w := utf8.DecodeRuneInString(id[i:])
		i += w
	}
	return id[:i] + "…"
}
