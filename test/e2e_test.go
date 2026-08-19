package test

import (
	"context"
	"encoding/json"
	"github.com/Forest-Isle/rc_wuqisen/internal/delivery"
	"github.com/Forest-Isle/rc_wuqisen/internal/domain"
	"github.com/Forest-Isle/rc_wuqisen/internal/migrate"
	"github.com/Forest-Isle/rc_wuqisen/internal/observability"
	"github.com/Forest-Isle/rc_wuqisen/internal/store"
	"github.com/Forest-Isle/rc_wuqisen/internal/target"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestPostgresDeliveryE2E(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	received := make(chan struct{}, 1)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Notification-ID") == "" {
			t.Error("missing notification id")
		}
		received <- struct{}{}
		w.WriteHeader(204)
	}))
	defer vendor.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, e := store.Open(ctx, url)
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
	r := domain.CreateRequest{URL: vendor.URL, Method: "POST", Headers: map[string]string{}, Body: json.RawMessage(`{"event":"e2e"}`)}
	h, _ := domain.CanonicalHash(r)
	n, _, e := s.Create(ctx, "e2e-key", r, h)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := s.Claim(ctx, 1, 30*time.Second)
	if e != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %+v", e, claimed)
	}
	n = claimed[0]
	m := observability.New(s)
	c := delivery.NewClient(target.Policy{AllowHTTP: true, AllowPrivate: true}, 2*time.Second)
	res := c.Deliver(ctx, n)
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	if e = s.MarkDelivered(ctx, n.ID, n.AttemptCount, res.Status); e != nil {
		t.Fatal(e)
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("vendor did not receive request")
	}
	got, e := s.Get(ctx, n.ID)
	if e != nil || got.Status != "delivered" {
		t.Fatalf("status: %+v %v", got, e)
	}
	_ = m
}
