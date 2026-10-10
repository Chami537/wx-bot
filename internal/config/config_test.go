package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValidAndDefaults(t *testing.T) {
	p := writeCfg(t, `{
  "bindings": [
    {
      "name": "xiaox",
      "webhook": { "url": "https://agent.example/hook" }
    }
  ]
}`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.StateDir, filepath.Join(".local", "state", "wx-bot")) {
		t.Fatalf("state_dir default %s", cfg.StateDir)
	}
	if cfg.LocalAPI.Listen != DefaultListen {
		t.Fatalf("listen %s", cfg.LocalAPI.Listen)
	}
	w := cfg.Bindings[0].Webhook
	if w.KeyHeader != "Authorization" || w.KeyPrefix != "Bearer " {
		t.Fatalf("webhook defaults %+v", w)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("WX_HOOK", "https://hooks.example/x")
	t.Setenv("WX_KEY", "sekrit")
	t.Setenv("WX_TOKEN", "local-token")
	p := writeCfg(t, `{
  "local_api": { "listen": "127.0.0.1:19999", "token": "env:WX_TOKEN" },
  "bindings": [{
    "name": "xiaox",
    "webhook": { "url": "env:WX_HOOK", "key": "env:WX_KEY", "secret": "env:WX_KEY" }
  }]
}`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Bindings[0]
	if b.Webhook.URL != "https://hooks.example/x" {
		t.Fatalf("url %s", b.Webhook.URL)
	}
	if b.Webhook.Key != "sekrit" || b.Webhook.Secret != "sekrit" {
		t.Fatalf("key not expanded")
	}
	if cfg.LocalAPI.Token != "local-token" {
		t.Fatalf("token %s", cfg.LocalAPI.Token)
	}
}

func TestDuplicateNames(t *testing.T) {
	p := writeCfg(t, `{
  "bindings": [
    {"name": "a", "webhook": {"url": "https://x"}},
    {"name": "a", "webhook": {"url": "https://y"}}
  ]
}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestBadName(t *testing.T) {
	p := writeCfg(t, `{"bindings":[{"name":"NOPE","webhook":{"url":"https://x"}}]}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("want invalid name, got %v", err)
	}
}

func TestNonLoopbackRejected(t *testing.T) {
	p := writeCfg(t, `{
  "local_api": {"listen": "0.0.0.0:18790"},
  "bindings": [{"name": "a", "webhook": {"url": "https://x"}}]
}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("want loopback error, got %v", err)
	}
	p = writeCfg(t, `{
  "local_api": {"listen": "8.8.8.8:80"},
  "bindings": [{"name": "a", "webhook": {"url": "https://x"}}]
}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("want loopback error for public IP, got %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	p := writeCfg(t, `{"nope": true, "bindings": []}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want unknown field, got %v", err)
	}
}

func TestUnsetEnvVarError(t *testing.T) {
	const name = "WXBOT_TEST_UNSET_ENV"
	os.Unsetenv(name)
	p := writeCfg(t, `{
  "bindings": [{"name": "xiaox", "webhook": {"url": "https://x", "key": "env:WXBOT_TEST_UNSET_ENV"}}]
}`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
	msg := err.Error()
	if !strings.Contains(msg, name) {
		t.Fatalf("error should name the variable: %v", err)
	}
	if !strings.Contains(msg, "webhook.key") {
		t.Fatalf("error should name the field: %v", err)
	}
}

func TestEmptyEnvVarError(t *testing.T) {
	t.Setenv("WXBOT_TEST_EMPTY_ENV", "")
	p := writeCfg(t, `{
  "bindings": [{"name": "xiaox", "webhook": {"url": "https://x", "secret": "env:WXBOT_TEST_EMPTY_ENV"}}]
}`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for empty env var")
	}
	msg := err.Error()
	if !strings.Contains(msg, "WXBOT_TEST_EMPTY_ENV") {
		t.Fatalf("error should name the variable: %v", err)
	}
	if !strings.Contains(msg, "webhook.secret") {
		t.Fatalf("error should name the field: %v", err)
	}
}

func TestCheckLoopbackListen(t *testing.T) {
	if err := CheckLoopbackListen("127.0.0.1:18790"); err != nil {
		t.Fatal(err)
	}
	if err := CheckLoopbackListen("[::1]:18790"); err != nil {
		t.Fatal(err)
	}
	if err := CheckLoopbackListen("localhost:9"); err != nil {
		t.Fatal(err)
	}
	if err := CheckLoopbackListen("192.168.1.1:80"); err == nil {
		t.Fatal("expected reject")
	}
}

func TestDeliveryDefaultAndPull(t *testing.T) {
	p := writeCfg(t, `{
  "bindings": [
    {"name": "a", "webhook": {"url": "https://x"}},
    {"name": "b", "delivery": "pull"}
  ]
}`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bindings[0].Delivery != DeliveryWebhook {
		t.Fatalf("default delivery %q", cfg.Bindings[0].Delivery)
	}
	if got := cfg.PullBindings(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("pull bindings %v", got)
	}
	if !strings.HasSuffix(cfg.InboxPath(), "inbox.db") {
		t.Fatalf("inbox path %s", cfg.InboxPath())
	}
}

func TestBadDelivery(t *testing.T) {
	p := writeCfg(t, `{"bindings": [{"name": "a", "delivery": "exec"}]}`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "delivery") {
		t.Fatalf("want delivery error, got %v", err)
	}
}
