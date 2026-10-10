package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/config"
	"github.com/lawrencewzen/wx-bot/internal/inbox"
)

func runWorker(t *testing.T, in *inbox.Inbox, url string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w := &WebhookWorker{Binding: "a", Inbox: in, Dispatcher: &Dispatcher{Webhook: config.Webhook{URL: url}, Attempts: 1}}
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func openInbox(t *testing.T) *inbox.Inbox {
	t.Helper()
	in, err := inbox.Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	return in
}

func waitStats(t *testing.T, in *inbox.Inbox, want inbox.Counts, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got, err := in.Stats(context.Background(), "a")
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats %+v, want %+v (err %v)", got, want, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWorkerRetriesServerErrorThenDelivers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	closeServer(t, srv)
	in := openInbox(t)
	in.Add(context.Background(), "a", "a:1", []byte("{}\n"))
	runWorker(t, in, srv.URL)

	// First attempt fails, retry follows after the 2s backoff.
	waitStats(t, in, inbox.Counts{}, 5*time.Second)
	if calls.Load() != 2 {
		t.Fatalf("calls %d", calls.Load())
	}
}

func TestWorkerGivesUpOnBadRequestWithoutBlocking(t *testing.T) {
	var got []string
	hits := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		hits <- string(buf[:n])
		if string(buf[:n]) == "bad\n" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	closeServer(t, srv)
	in := openInbox(t)
	in.Add(context.Background(), "a", "a:1", []byte("bad\n"))
	in.Add(context.Background(), "a", "a:2", []byte("good\n"))
	runWorker(t, in, srv.URL)

	waitStats(t, in, inbox.Counts{Dead: 1}, 3*time.Second)
	for len(got) < 2 {
		select {
		case b := <-hits:
			got = append(got, b)
		case <-time.After(time.Second):
			t.Fatalf("hits %v", got)
		}
	}
	if got[0] != "bad\n" || got[1] != "good\n" {
		t.Fatalf("delivery order %q", got)
	}
}

func TestRetryBackoff(t *testing.T) {
	cases := map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 20: 5 * time.Minute}
	for attempt, want := range cases {
		if got := retryBackoff(attempt); got != want {
			t.Errorf("retryBackoff(%d) = %v, want %v", attempt, got, want)
		}
	}
}
