package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/inbox"
)

func startEvents(t *testing.T, in *inbox.Inbox, pull []string) string {
	t.Helper()
	ready := make(chan string, 1)
	s := &Server{
		Listen:       "127.0.0.1:0",
		Token:        "tok",
		Inbox:        in,
		PullBindings: pull,
		OnListen:     func(addr string) { ready <- addr },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Start(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case addr := <-ready:
		return "http://" + addr
	case <-time.After(2 * time.Second):
		t.Fatal("listen timeout")
		return ""
	}
}

func call(t *testing.T, method, url, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestEventsPullLeaseAck(t *testing.T) {
	in, err := inbox.Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	ctx := context.Background()
	in.Add(ctx, "p", "p:1", []byte(`{"event_id":"p:1"}`+"\n"))
	in.Add(ctx, "w", "w:1", []byte(`{"event_id":"w:1"}`+"\n"))
	base := startEvents(t, in, []string{"p"})

	code, out := call(t, "GET", base+"/v1/events", "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var events []map[string]string
	json.Unmarshal(out["events"], &events)
	if len(events) != 1 || events[0]["event_id"] != "p:1" {
		t.Fatalf("events %s (webhook binding must not be served)", out["events"])
	}

	// Leased: a second pull is empty.
	_, out = call(t, "GET", base+"/v1/events", "")
	if string(out["events"]) != "[]" {
		t.Fatalf("second pull %s", out["events"])
	}

	// Cannot ack another binding's event.
	_, out = call(t, "POST", base+"/v1/events/ack", `{"event_ids":["w:1"]}`)
	if string(out["acked"]) != "0" {
		t.Fatalf("acked webhook event: %s", out["acked"])
	}
	code, out = call(t, "POST", base+"/v1/events/ack", `{"event_ids":["p:1"]}`)
	if code != 200 || string(out["acked"]) != "1" {
		t.Fatalf("ack %d %s", code, out["acked"])
	}

	if code, _ := call(t, "GET", base+"/v1/events?binding=w", ""); code != 404 {
		t.Fatalf("pull of webhook binding status %d", code)
	}
	if code, _ := call(t, "GET", base+"/v1/events?wait=10m", ""); code != 400 {
		t.Fatalf("oversized wait status %d", code)
	}
}

func TestEventsLongPollWakes(t *testing.T) {
	in, err := inbox.Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	base := startEvents(t, in, []string{"p"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		in.Add(context.Background(), "p", "p:9", []byte(`{"event_id":"p:9"}`))
	}()
	start := time.Now()
	_, out := call(t, "GET", base+"/v1/events?wait=5s", "")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("long poll took %v", d)
	}
	var events []map[string]string
	json.Unmarshal(out["events"], &events)
	if len(events) != 1 || events[0]["event_id"] != "p:9" {
		t.Fatalf("events %s", out["events"])
	}
}

func TestEventsRequireToken(t *testing.T) {
	in, err := inbox.Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	base := startEvents(t, in, []string{"p"})
	resp, err := http.Get(base + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
