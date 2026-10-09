package bridge

import (
	"sort"
	"unicode/utf8"

	"github.com/lawrencewzen/wx-bot/internal/config"
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
	WebhookHost string   `json:"webhook_host"`
}

// StatusOf computes status for one configured binding from disk.
func StatusOf(cfg *config.Config, b config.Binding) BindingStatus {
	st := BindingStatus{
		Name:        b.Name,
		WebhookHost: Host(b.Webhook.URL),
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
func StatusAll(cfg *config.Config) []BindingStatus {
	out := make([]BindingStatus, 0, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		out = append(out, StatusOf(cfg, b))
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
