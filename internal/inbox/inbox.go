// Package inbox is the durable SQLite queue between the iLink poller and
// agent delivery (webhook push or pull). Every accepted WeChat message is
// written here before the iLink cursor advances, so a delivery failure or
// crash never loses it.
package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	StatePending = "pending"
	StateDone    = "done"
	StateDead    = "dead"

	// pollInterval bounds how long a waiter sleeps without an in-process
	// notification; it is how other processes' writes are noticed.
	pollInterval = 500 * time.Millisecond
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id    TEXT    NOT NULL UNIQUE,
	binding     TEXT    NOT NULL,
	payload     BLOB    NOT NULL,
	state       TEXT    NOT NULL DEFAULT 'pending',
	created_at  INTEGER NOT NULL,
	attempts    INTEGER NOT NULL DEFAULT 0,
	next_at     INTEGER NOT NULL DEFAULT 0,
	lease_until INTEGER NOT NULL DEFAULT 0,
	last_error  TEXT    NOT NULL DEFAULT '',
	done_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS events_ready ON events (state, binding, seq);
`

// Event is one queued message. Payload is the exact JSON body sent to agents.
type Event struct {
	Seq       int64
	EventID   string
	Binding   string
	Payload   json.RawMessage
	State     string
	CreatedAt time.Time
	Attempts  int
	LastError string
}

// Counts summarises one binding's queue.
type Counts struct {
	Pending int `json:"pending"`
	Dead    int `json:"dead"`
}

// Inbox is safe for concurrent use and for use from several processes.
type Inbox struct {
	db  *sql.DB
	now func() time.Time

	mu      sync.Mutex
	changed chan struct{}
}

// Open opens (creating if needed, mode 0600) the inbox database at path.
func Open(path string) (*Inbox, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open inbox: %w", err)
	}
	f.Close()
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open inbox: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init inbox: %w", err)
	}
	return &Inbox{db: db, now: time.Now, changed: make(chan struct{})}, nil
}

// Close closes the database.
func (in *Inbox) Close() error { return in.db.Close() }

func (in *Inbox) ms() int64 { return in.now().UnixMilli() }

// signal wakes every in-process waiter.
func (in *Inbox) signal() {
	in.mu.Lock()
	close(in.changed)
	in.changed = make(chan struct{})
	in.mu.Unlock()
}

// Changed returns a channel closed on the next in-process write.
func (in *Inbox) Changed() <-chan struct{} {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.changed
}

// Add queues an event. A duplicate event_id is ignored and reports false.
func (in *Inbox) Add(ctx context.Context, binding, eventID string, payload []byte) (bool, error) {
	res, err := in.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events (event_id, binding, payload, created_at) VALUES (?, ?, ?, ?)`,
		eventID, binding, payload, in.ms())
	if err != nil {
		return false, fmt.Errorf("inbox add: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		in.signal()
	}
	return n > 0, nil
}

// Claim leases up to limit ready events of the given bindings, oldest first.
// A leased event is invisible to other claims until it is acked, retried,
// or its lease expires.
func (in *Inbox) Claim(ctx context.Context, bindings []string, limit int, lease time.Duration) ([]Event, error) {
	if len(bindings) == 0 || limit <= 0 {
		return nil, nil
	}
	tx, err := in.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("inbox claim: %w", err)
	}
	defer tx.Rollback()

	now := in.ms()
	args := []any{StatePending, now, now}
	for _, b := range bindings {
		args = append(args, b)
	}
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, `
		SELECT seq, event_id, binding, payload, state, created_at, attempts, last_error
		FROM events
		WHERE state = ? AND next_at <= ? AND lease_until <= ? AND binding IN (`+placeholders(len(bindings))+`)
		ORDER BY seq LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("inbox claim: %w", err)
	}
	events, err := scan(rows)
	if err != nil {
		return nil, err
	}
	until := now + lease.Milliseconds()
	for i := range events {
		if _, err := tx.ExecContext(ctx,
			`UPDATE events SET lease_until = ?, attempts = attempts + 1 WHERE seq = ?`,
			until, events[i].Seq); err != nil {
			return nil, fmt.Errorf("inbox claim: %w", err)
		}
		events[i].Attempts++
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("inbox claim: %w", err)
	}
	return events, nil
}

// Count reports how many events of the given bindings are ready to claim now.
func (in *Inbox) Count(ctx context.Context, bindings []string) (int, error) {
	if len(bindings) == 0 {
		return 0, nil
	}
	now := in.ms()
	args := []any{StatePending, now, now}
	for _, b := range bindings {
		args = append(args, b)
	}
	var n int
	err := in.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE state = ? AND next_at <= ? AND lease_until <= ? AND binding IN (`+placeholders(len(bindings))+`)`,
		args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("inbox count: %w", err)
	}
	return n, nil
}

// Stats reports pending (including leased) and dead totals for one binding.
func (in *Inbox) Stats(ctx context.Context, binding string) (Counts, error) {
	var c Counts
	err := in.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(state = 'pending'), 0),
			COALESCE(SUM(state = 'dead'), 0)
		FROM events WHERE binding = ?`, binding).Scan(&c.Pending, &c.Dead)
	if err != nil {
		return Counts{}, fmt.Errorf("inbox stats: %w", err)
	}
	return c, nil
}

// Ack marks events delivered. Unknown or already-finished ids are skipped;
// it returns how many events changed. When bindings is non-empty, only
// events of those bindings can be acked.
func (in *Inbox) Ack(ctx context.Context, bindings []string, eventIDs []string) (int, error) {
	if len(eventIDs) == 0 {
		return 0, nil
	}
	q := `UPDATE events SET state = ?, done_at = ?, lease_until = 0
		WHERE state = ? AND event_id IN (` + placeholders(len(eventIDs)) + `)`
	args := []any{StateDone, in.ms(), StatePending}
	for _, id := range eventIDs {
		args = append(args, id)
	}
	if len(bindings) > 0 {
		q += ` AND binding IN (` + placeholders(len(bindings)) + `)`
		for _, b := range bindings {
			args = append(args, b)
		}
	}
	res, err := in.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("inbox ack: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Retry releases a leased event so it becomes ready again at `at`.
func (in *Inbox) Retry(ctx context.Context, eventID, reason string, at time.Time) error {
	_, err := in.db.ExecContext(ctx,
		`UPDATE events SET lease_until = 0, next_at = ?, last_error = ? WHERE event_id = ? AND state = ?`,
		at.UnixMilli(), reason, eventID, StatePending)
	if err != nil {
		return fmt.Errorf("inbox retry: %w", err)
	}
	return nil
}

// Dead gives up on an event; it is kept for inspection until pruned.
func (in *Inbox) Dead(ctx context.Context, eventID, reason string) error {
	_, err := in.db.ExecContext(ctx,
		`UPDATE events SET state = ?, done_at = ?, lease_until = 0, last_error = ? WHERE event_id = ? AND state = ?`,
		StateDead, in.ms(), reason, eventID, StatePending)
	if err != nil {
		return fmt.Errorf("inbox dead: %w", err)
	}
	return nil
}

// Prune deletes finished events older than retention and gives up on
// pending events that have waited longer than retention.
func (in *Inbox) Prune(ctx context.Context, retention time.Duration) (expired, deleted int64, err error) {
	cutoff := in.ms() - retention.Milliseconds()
	res, err := in.db.ExecContext(ctx,
		`UPDATE events SET state = ?, done_at = ?, lease_until = 0, last_error = 'expired before delivery'
		 WHERE state = ? AND created_at < ?`,
		StateDead, in.ms(), StatePending, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("inbox prune: %w", err)
	}
	expired, _ = res.RowsAffected()
	res, err = in.db.ExecContext(ctx,
		`DELETE FROM events WHERE state IN (?, ?) AND done_at < ?`, StateDone, StateDead, cutoff)
	if err != nil {
		return expired, 0, fmt.Errorf("inbox prune: %w", err)
	}
	deleted, _ = res.RowsAffected()
	return expired, deleted, nil
}

// Wait blocks until an in-process write, the poll interval, or ctx ends.
func (in *Inbox) Wait(ctx context.Context) error {
	t := time.NewTimer(pollInterval)
	defer t.Stop()
	select {
	case <-in.Changed():
		return nil
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ClaimWait is Claim, but when nothing is ready it keeps trying until wait
// elapses. wait <= 0 returns immediately.
func (in *Inbox) ClaimWait(ctx context.Context, bindings []string, limit int, lease, wait time.Duration) ([]Event, error) {
	deadline := in.now().Add(wait)
	for {
		events, err := in.Claim(ctx, bindings, limit, lease)
		if err != nil || len(events) > 0 || !in.now().Before(deadline) {
			return events, err
		}
		wctx, cancel := context.WithDeadline(ctx, deadline)
		err = in.Wait(wctx)
		cancel()
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

func scan(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var created int64
		var payload []byte
		if err := rows.Scan(&e.Seq, &e.EventID, &e.Binding, &payload, &e.State, &created, &e.Attempts, &e.LastError); err != nil {
			return nil, fmt.Errorf("inbox scan: %w", err)
		}
		e.Payload = json.RawMessage(payload)
		e.CreatedAt = time.UnixMilli(created)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inbox scan: %w", err)
	}
	return out, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// Limits for pull requests (CLI and HTTP).
const (
	DefaultPullLimit = 20
	MaxPullLimit     = 100
	DefaultLease     = 5 * time.Minute
	MaxLease         = time.Hour
	MaxWait          = 60 * time.Second
)

// Scope narrows the pull-enabled bindings to want ("" means all of them).
func Scope(pull []string, want string) ([]string, error) {
	if len(pull) == 0 {
		return nil, fmt.Errorf("no binding has \"delivery\": \"pull\"")
	}
	if want == "" {
		return pull, nil
	}
	for _, b := range pull {
		if b == want {
			return []string{want}, nil
		}
	}
	return nil, fmt.Errorf("binding %q does not use pull delivery", want)
}

// Payloads returns the event bodies for a pull response.
func Payloads(events []Event) []json.RawMessage {
	out := make([]json.RawMessage, len(events))
	for i, e := range events {
		out[i] = e.Payload
	}
	return out
}
