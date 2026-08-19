package delivery

import (
	"context"
	"github.com/Forest-Isle/rc_wuqisen/internal/domain"
	"github.com/Forest-Isle/rc_wuqisen/internal/observability"
	"github.com/Forest-Isle/rc_wuqisen/internal/store"
	"log/slog"
	"sync"
	"time"
)

type Worker struct {
	store                     store.Store
	client                    *Client
	metrics                   *observability.Metrics
	log                       *slog.Logger
	workers, maxAttempts      int
	poll, lease, initial, max time.Duration
}

func NewWorker(s store.Store, c *Client, m *observability.Metrics, l *slog.Logger, workers, maxAttempts int, poll, lease, initial, max time.Duration) *Worker {
	return &Worker{s, c, m, l, workers, maxAttempts, poll, lease, initial, max}
}
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.loop(ctx) }()
	}
	wg.Wait()
}
func (w *Worker) loop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		jobs, e := w.store.Claim(ctx, 1, w.lease)
		if e != nil {
			if ctx.Err() == nil {
				w.log.Error("claim notifications", "error", e)
			}
			timer.Reset(w.poll)
			continue
		}
		if len(jobs) == 0 {
			timer.Reset(w.poll)
			continue
		}
		processCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.lease)
		w.process(processCtx, jobs[0])
		cancel()
		timer.Reset(0)
	}
}
func (w *Worker) process(ctx context.Context, n domain.Notification) {
	res := w.client.Deliver(ctx, n)
	w.metrics.Duration.Observe(res.Duration.Seconds())
	code := res.Status
	var cp *int
	if code != 0 {
		cp = &code
	}
	if res.Error == "" {
		w.metrics.Attempts.WithLabelValues("delivered").Inc()
		if e := w.store.MarkDelivered(ctx, n.ID, n.AttemptCount, res.Status); e != nil {
			w.log.Error("mark delivered", "notification_id", n.ID, "error", e)
		}
		return
	}
	outcome := "retry"
	if !res.Retryable || n.AttemptCount >= w.maxAttempts {
		outcome = "dead"
		if e := w.store.MarkDead(ctx, n.ID, n.AttemptCount, res.Error, cp); e != nil {
			w.log.Error("mark dead", "notification_id", n.ID, "error", e)
		}
	} else {
		d := Delay(n.AttemptCount, w.initial, w.max)
		if res.RetryAfter > d {
			d = res.RetryAfter
		}
		if d > 24*time.Hour {
			d = 24 * time.Hour
		}
		if e := w.store.ScheduleRetry(ctx, n.ID, n.AttemptCount, time.Now().UTC().Add(d), res.Error, cp); e != nil {
			w.log.Error("schedule retry", "notification_id", n.ID, "error", e)
		}
	}
	w.metrics.Attempts.WithLabelValues(outcome).Inc()
	w.log.Info("delivery attempt", "notification_id", n.ID, "attempt", n.AttemptCount, "outcome", outcome, "status", res.Status, "duration_ms", res.Duration.Milliseconds())
}
