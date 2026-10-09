package bridge

import (
	"fmt"

	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/ilink"
	"github.com/lawrencewzen/wx-bot/internal/state"
)

// LiveBinding is the in-memory handle for one logged-in binding.
type LiveBinding struct {
	Name    string
	Config  config.Binding
	Store   *state.Store
	Session *state.Session
	Client  *ilink.Client
	Allow   Allowlist
}

// OpenLive loads session + tokens from disk. Returns an error if missing or expired.
func OpenLive(cfg *config.Config, name string) (*LiveBinding, error) {
	b, ok := cfg.Binding(name)
	if !ok {
		return nil, fmt.Errorf("unknown binding %q", name)
	}
	store, err := state.Open(cfg.StateDir, name)
	if err != nil {
		return nil, err
	}
	if store.IsExpired() {
		return nil, fmt.Errorf("binding %s session expired: run `wx-bot login %s`", name, name)
	}
	sess, err := store.LoadSession()
	if err != nil {
		return nil, fmt.Errorf("binding %s: %w (run `wx-bot login %s`)", name, err, name)
	}
	client := ilink.NewClient(sess.ToIlink())
	tokens, err := store.LoadTokens()
	if err != nil {
		return nil, err
	}
	client.LoadTokens(tokens)
	allow := NewAllowlist(b.AllowWxids, sess.UserID)
	return &LiveBinding{
		Name:    name,
		Config:  b,
		Store:   store,
		Session: sess,
		Client:  client,
		Allow:   allow,
	}, nil
}

// Sender returns a Sender bound to this live client.
func (l *LiveBinding) Sender() Sender {
	return Sender{Client: l.Client, Allow: l.Allow}
}
