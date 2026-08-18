package store

import (
	"context"
	"github.com/wuqisen/reliable-notification-service/internal/domain"
	"github.com/wuqisen/reliable-notification-service/internal/migrate"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, e := Open(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = migrate.Run(ctx, s.Pool()); e != nil {
		t.Fatal(e)
	}
	lock, e := s.Pool().Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Release()
	if _, e = lock.Exec(ctx, "SELECT pg_advisory_lock(987654321)"); e != nil {
		t.Fatal(e)
	}
	defer lock.Exec(context.Background(), "SELECT pg_advisory_unlock(987654321)")
	_, _ = s.Pool().Exec(ctx, "TRUNCATE notifications")
	r := domain.CreateRequest{URL: "https://example.com/hook", Method: "POST", Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"x":1}`)}
	h := []byte("hash")
	n, re, e := s.Create(ctx, "integration-key", r, h)
	if e != nil || re || n.ID == "" {
		t.Fatalf("create: %+v %v %v", n, re, e)
	}
	same, re, e := s.Create(ctx, "integration-key", r, h)
	if e != nil || !re || same.ID != n.ID {
		t.Fatalf("replay: %+v %v %v", same, re, e)
	}
	if _, _, e = s.Create(ctx, "integration-key", r, []byte("different")); e != ErrIdempotencyConflict {
		t.Fatalf("expected conflict, got %v", e)
	}
	jobs, e := s.Claim(ctx, 1, 30*time.Second)
	if e != nil || len(jobs) != 1 || jobs[0].AttemptCount != 1 {
		t.Fatalf("claim: %v %+v", e, jobs)
	}
	if _, e = s.Pool().Exec(ctx, "UPDATE notifications SET lease_until=now()-interval '1 second' WHERE id=$1", n.ID); e != nil {
		t.Fatal(e)
	}
	reclaimed, e := s.Claim(ctx, 1, 30*time.Second)
	if e != nil || len(reclaimed) != 1 || reclaimed[0].AttemptCount != 2 {
		t.Fatalf("reclaim: %v %+v", e, reclaimed)
	}
	if e = s.MarkDead(ctx, n.ID, jobs[0].AttemptCount, "stale", nil); e != ErrLeaseLost {
		t.Fatalf("expected stale attempt fencing, got %v", e)
	}
	if e = s.MarkDelivered(ctx, n.ID, reclaimed[0].AttemptCount, 204); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.Claim(ctx, 1, time.Second)
	if e != nil || len(jobs) != 0 {
		t.Fatalf("terminal reclaimed: %v %+v", e, jobs)
	}
	_, _ = s.Pool().Exec(ctx, "TRUNCATE notifications")
	if _, _, e = s.Create(ctx, "concurrent-key", r, []byte("concurrent")); e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, claimErr := s.Claim(ctx, 1, 30*time.Second)
			if claimErr != nil {
				t.Errorf("concurrent claim: %v", claimErr)
				return
			}
			results <- len(claimed)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	total := 0
	for count := range results {
		total += count
	}
	if total != 1 {
		t.Fatalf("expected exactly one concurrent claim, got %d", total)
	}
}
