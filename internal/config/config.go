package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	DefaultListen    = "127.0.0.1:18790"
	defaultKeyHeader = "Authorization"
	defaultKeyPrefix = "Bearer "
)

var bindingNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// Config is the wx-bot JSON configuration.
type Config struct {
	StateDir string    `json:"state_dir"`
	MediaDir string    `json:"media_dir"`
	LocalAPI LocalAPI  `json:"local_api"`
	Bindings []Binding `json:"bindings"`
}

// LocalAPI is the loopback-only HTTP send API.
type LocalAPI struct {
	Listen string `json:"listen"`
	Token  string `json:"token"`
}

// Binding maps one WeChat account to one webhook.
type Binding struct {
	Name             string   `json:"name"`
	Webhook          Webhook  `json:"webhook"`
	AllowWxids       []string `json:"allow_wxids"`
	ForwardUnallowed bool     `json:"forward_unallowed"`
}

// Webhook is the outbound agent webhook for a binding.
type Webhook struct {
	URL       string `json:"url"`
	Key       string `json:"key"`
	KeyHeader string `json:"key_header"`
	KeyPrefix string `json:"key_prefix"`
	Secret    string `json:"secret"`
}

// DefaultPath returns ~/.config/wx-bot/config.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(home, ".config", "wx-bot", "config.json")
}

// DefaultStateDir returns ~/.local/state/wx-bot.
func DefaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "state"
	}
	return filepath.Join(home, ".local", "state", "wx-bot")
}

// Load reads, expands, defaults, and validates a config file.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.expand(); err != nil {
		return nil, err
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) expand() error {
	var err error
	if c.StateDir, err = expandString(c.StateDir); err != nil {
		return fmt.Errorf("state_dir: %w", err)
	}
	if c.MediaDir, err = expandString(c.MediaDir); err != nil {
		return fmt.Errorf("media_dir: %w", err)
	}
	if c.LocalAPI.Listen, err = expandString(c.LocalAPI.Listen); err != nil {
		return fmt.Errorf("local_api.listen: %w", err)
	}
	if c.LocalAPI.Token, err = expandString(c.LocalAPI.Token); err != nil {
		return fmt.Errorf("local_api.token: %w", err)
	}
	for i := range c.Bindings {
		b := &c.Bindings[i]
		if b.Name, err = expandString(b.Name); err != nil {
			return fmt.Errorf("bindings[%d].name: %w", i, err)
		}
		if b.Webhook.URL, err = expandString(b.Webhook.URL); err != nil {
			return fmt.Errorf("bindings[%d].webhook.url: %w", i, err)
		}
		if b.Webhook.Key, err = expandString(b.Webhook.Key); err != nil {
			return fmt.Errorf("bindings[%d].webhook.key: %w", i, err)
		}
		if b.Webhook.KeyHeader, err = expandString(b.Webhook.KeyHeader); err != nil {
			return fmt.Errorf("bindings[%d].webhook.key_header: %w", i, err)
		}
		if b.Webhook.KeyPrefix, err = expandString(b.Webhook.KeyPrefix); err != nil {
			return fmt.Errorf("bindings[%d].webhook.key_prefix: %w", i, err)
		}
		if b.Webhook.Secret, err = expandString(b.Webhook.Secret); err != nil {
			return fmt.Errorf("bindings[%d].webhook.secret: %w", i, err)
		}
		for j := range b.AllowWxids {
			if b.AllowWxids[j], err = expandString(b.AllowWxids[j]); err != nil {
				return fmt.Errorf("bindings[%d].allow_wxids[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir()
	}
	if c.MediaDir == "" {
		c.MediaDir = filepath.Join(c.StateDir, "media")
	}
	if c.LocalAPI.Listen == "" {
		c.LocalAPI.Listen = DefaultListen
	}
	for i := range c.Bindings {
		w := &c.Bindings[i].Webhook
		if w.KeyHeader == "" {
			w.KeyHeader = defaultKeyHeader
		}
		if w.KeyPrefix == "" {
			w.KeyPrefix = defaultKeyPrefix
		}
	}
}

// Validate checks names, uniqueness, and loopback listen address.
func (c *Config) Validate() error {
	if err := CheckLoopbackListen(c.LocalAPI.Listen); err != nil {
		return err
	}
	seen := make(map[string]int, len(c.Bindings))
	for i, b := range c.Bindings {
		if !bindingNameRe.MatchString(b.Name) {
			return fmt.Errorf("bindings[%d].name %q invalid: must match [a-z0-9_-]{1,32}", i, b.Name)
		}
		if prev, ok := seen[b.Name]; ok {
			return fmt.Errorf("duplicate binding name %q (index %d and %d)", b.Name, prev, i)
		}
		seen[b.Name] = i
	}
	return nil
}

// Binding returns the named binding.
func (c *Config) Binding(name string) (Binding, bool) {
	for _, b := range c.Bindings {
		if b.Name == name {
			return b, true
		}
	}
	return Binding{}, false
}

// CheckLoopbackListen reports whether addr is host:port on a loopback address.
func CheckLoopbackListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("local_api.listen %q: %w", addr, err)
	}
	if port == "" {
		return fmt.Errorf("local_api.listen %q: missing port", addr)
	}
	if host == "" {
		return fmt.Errorf("local_api.listen %q is not a loopback address (refusing unspecified bind)", addr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("local_api.listen %q is not a loopback address", addr)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("local_api.listen %q: resolve: %w", addr, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("local_api.listen %q did not resolve", addr)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return fmt.Errorf("local_api.listen %q is not a loopback address", addr)
		}
	}
	return nil
}

func expandString(s string) (string, error) {
	if strings.HasPrefix(s, "env:") {
		name := strings.TrimPrefix(s, "env:")
		if name == "" {
			return "", fmt.Errorf("empty env var name")
		}
		s = os.Getenv(name)
	}
	return expandHome(s), nil
}

func expandHome(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return s
		}
		return home + s[1:]
	}
	return s
}
