package bridge

import (
	"context"
	"log/slog"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/inbox"
)

const (
	defaultDeliverLease = 2 * time.Minute
	deliverIdle         = time.Second
	deliverMaxBackoff   = 5 * time.Minute
)

// WebhookWorker drains one binding's inbox to its webhook, oldest first.
// A failed event is retried with backoff without blocking later events.
type WebhookWorker struct {
	Binding    string
	Inbox      *inbox.Inbox
	Dispatcher *Dispatcher
	Lease      time.Duration
	Log        *slog.Logger
}

func (w *WebhookWorker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default().With("component", "webhook", "binding", w.Binding)
}

func (w *WebhookWorker) lease() time.Duration {
	if w.Lease > 0 {
		return w.Lease
	}
	return defaultDeliverLease
}

// Run delivers until ctx is cancelled.
func (w *WebhookWorker) Run(ctx context.Context) {
	log := w.log()
	for ctx.Err() == nil {
		changed := w.Inbox.Changed()
		events, err := w.Inbox.Claim(ctx, []string{w.Binding}, 1, w.lease())
		if err != nil {
			if ctx.Err() == nil {
				log.Error("claim", "err", err)
			}
			w.idle(ctx, nil)
			continue
		}
		if len(events) == 0 {
			w.idle(ctx, changed)
			continue
		}
		w.deliver(ctx, events[0])
	}
}

func (w *WebhookWorker) idle(ctx context.Context, changed <-chan struct{}) {
	t := time.NewTimer(deliverIdle)
	defer t.Stop()
	select {
	case <-changed:
	case <-t.C:
	case <-ctx.Done():
	}
}

func (w *WebhookWorker) deliver(ctx context.Context, ev inbox.Event) {
	log := w.log().With("event_id", ev.EventID, "attempt", ev.Attempts)
	err := w.Dispatcher.PostBody(ctx, ev.Payload)
	// Use a fresh context so the outcome is recorded even during shutdown.
	rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch {
	case err == nil:
		if _, err := w.Inbox.Ack(rctx, nil, []string{ev.EventID}); err != nil {
			log.Error("mark delivered", "err", err)
		}
	case ctx.Err() != nil:
		// Shutting down: release the lease so the next run retries at once.
		if err := w.Inbox.Retry(rctx, ev.EventID, "interrupted", time.Now()); err != nil {
			log.Error("release lease", "err", err)
		}
	case Permanent(err):
		log.Error("webhook rejected event; giving up", "err", err)
		if err := w.Inbox.Dead(rctx, ev.EventID, err.Error()); err != nil {
			log.Error("mark dead", "err", err)
		}
	default:
		delay := retryBackoff(ev.Attempts)
		log.Warn("webhook delivery failed; will retry", "err", err, "in", delay.String())
		if err := w.Inbox.Retry(rctx, ev.EventID, err.Error(), time.Now().Add(delay)); err != nil {
			log.Error("schedule retry", "err", err)
		}
	}
}

// retryBackoff is 2s, 4s, 8s ... capped at 5 minutes.
func retryBackoff(attempt int) time.Duration {
	d := 2 * time.Second
	for i := 1; i < attempt && d < deliverMaxBackoff; i++ {
		d *= 2
	}
	if d > deliverMaxBackoff {
		d = deliverMaxBackoff
	}
	return d
}
