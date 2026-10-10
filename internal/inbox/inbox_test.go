package inbox

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openTest(t *testing.T) (*Inbox, *clock) {
	t.Helper()
	in, err := Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	c := &clock{t: time.Unix(1760000000, 0)}
	in.now = c.now
	return in, c
}

func ids(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.EventID
	}
	return out
}

func TestAddDeduplicates(t *testing.T) {
	in, _ := openTest(t)
	ctx := context.Background()
	added, err := in.Add(ctx, "a", "a:1", []byte(`{"n":1}`))
	if err != nil || !added {
		t.Fatalf("first add = %v, %v", added, err)
	}
	added, err = in.Add(ctx, "a", "a:1", []byte(`{"n":2}`))
	if err != nil || added {
		t.Fatalf("duplicate add = %v, %v", added, err)
	}
	evs, err := in.Claim(ctx, []string{"a"}, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || string(evs[0].Payload) != `{"n":1}` {
		t.Fatalf("events %+v", evs)
	}
}

func TestFileMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	in, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestClaimLeaseAckAndExpiry(t *testing.T) {
	in, clk := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"a:1", "a:2", "b:1"} {
		if _, err := in.Add(ctx, id[:1], id, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}

	evs, err := in.Claim(ctx, []string{"a"}, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(evs); len(got) != 2 || got[0] != "a:1" || got[1] != "a:2" {
		t.Fatalf("claimed %v", got)
	}
	if evs[0].Attempts != 1 {
		t.Fatalf("attempts %d", evs[0].Attempts)
	}
	// Leased events are hidden.
	if n, _ := in.Count(ctx, []string{"a"}); n != 0 {
		t.Fatalf("count while leased = %d", n)
	}
	if n, err := in.Ack(ctx, nil, []string{"a:1"}); err != nil || n != 1 {
		t.Fatalf("ack = %d, %v", n, err)
	}
	// Ack is idempotent.
	if n, _ := in.Ack(ctx, nil, []string{"a:1"}); n != 0 {
		t.Fatalf("second ack = %d", n)
	}

	clk.add(2 * time.Minute)
	evs, _ = in.Claim(ctx, []string{"a"}, 10, time.Minute)
	if got := ids(evs); len(got) != 1 || got[0] != "a:2" || evs[0].Attempts != 2 {
		t.Fatalf("after lease expiry %v attempts=%d", got, evs[0].Attempts)
	}
	st, _ := in.Stats(ctx, "a")
	if st.Pending != 1 || st.Dead != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestAckRestrictedToBindings(t *testing.T) {
	in, _ := openTest(t)
	ctx := context.Background()
	in.Add(ctx, "a", "a:1", []byte(`{}`))
	if n, _ := in.Ack(ctx, []string{"b"}, []string{"a:1"}); n != 0 {
		t.Fatalf("acked event of another binding: %d", n)
	}
}

func TestRetryAndDead(t *testing.T) {
	in, clk := openTest(t)
	ctx := context.Background()
	in.Add(ctx, "a", "a:1", []byte(`{}`))
	in.Add(ctx, "a", "a:2", []byte(`{}`))
	in.Claim(ctx, []string{"a"}, 2, time.Minute)

	if err := in.Retry(ctx, "a:1", "boom", clk.now().Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := in.Dead(ctx, "a:2", "bad request"); err != nil {
		t.Fatal(err)
	}
	if n, _ := in.Count(ctx, []string{"a"}); n != 0 {
		t.Fatalf("retry should wait until next_at, count=%d", n)
	}
	clk.add(11 * time.Second)
	evs, _ := in.Claim(ctx, []string{"a"}, 10, time.Minute)
	if got := ids(evs); len(got) != 1 || got[0] != "a:1" || evs[0].LastError != "boom" {
		t.Fatalf("after retry %+v", evs)
	}
	st, _ := in.Stats(ctx, "a")
	if st.Pending != 1 || st.Dead != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestPrune(t *testing.T) {
	in, clk := openTest(t)
	ctx := context.Background()
	in.Add(ctx, "a", "a:old-pending", []byte(`{}`))
	in.Add(ctx, "a", "a:old-done", []byte(`{}`))
	in.Ack(ctx, nil, []string{"a:old-done"})
	clk.add(8 * 24 * time.Hour)
	in.Add(ctx, "a", "a:new", []byte(`{}`))

	expired, deleted, err := in.Prune(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 || deleted != 1 {
		t.Fatalf("expired=%d deleted=%d", expired, deleted)
	}
	st, _ := in.Stats(ctx, "a")
	if st.Pending != 1 || st.Dead != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestClaimWaitWakesOnAdd(t *testing.T) {
	in, err := Open(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	ctx := context.Background()
	go func() {
		time.Sleep(50 * time.Millisecond)
		in.Add(ctx, "a", "a:1", []byte(`{}`))
	}()
	start := time.Now()
	evs, err := in.ClaimWait(ctx, []string{"a"}, 1, time.Minute, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("events %v", evs)
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("woke after %v, want prompt wake", d)
	}
	evs, err = in.ClaimWait(ctx, []string{"a"}, 1, time.Minute, 100*time.Millisecond)
	if err != nil || len(evs) != 0 {
		t.Fatalf("empty wait = %v, %v", evs, err)
	}
}

func TestTwoHandlesShareQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	a.Add(ctx, "x", "x:1", []byte(`{}`))
	evs, err := b.ClaimWait(ctx, []string{"x"}, 1, time.Minute, 2*time.Second)
	if err != nil || len(evs) != 1 {
		t.Fatalf("other handle claim = %v, %v", evs, err)
	}
	if again, _ := a.Claim(ctx, []string{"x"}, 1, time.Minute); len(again) != 0 {
		t.Fatalf("event claimed twice across handles")
	}
}
