package delivery

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Forest-Isle/rc_wuqisen/internal/domain"
	"github.com/Forest-Isle/rc_wuqisen/internal/observability"
	"github.com/Forest-Isle/rc_wuqisen/internal/target"
)

func TestRetryClassification(t *testing.T) {
	for _, code := range []int{408, 425, 429, 500, 599} {
		if !RetryableStatus(code) {
			t.Errorf("%d should retry", code)
		}
	}
	for _, code := range []int{301, 400, 401, 404} {
		if RetryableStatus(code) {
			t.Errorf("%d should be permanent", code)
		}
	}
}
func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := http.Header{"Retry-After": []string{"7"}}
	if RetryAfter(h, now) != 7*time.Second {
		t.Fatal("seconds Retry-After failed")
	}
	h.Set("Retry-After", now.Add(2*time.Minute).Format(http.TimeFormat))
	if RetryAfter(h, now) != 2*time.Minute {
		t.Fatal("date Retry-After failed")
	}
}
func TestDelayBounded(t *testing.T) {
	d := Delay(20, time.Second, 5*time.Second)
	if d < 5*time.Second || d > 6250*time.Millisecond {
		t.Fatalf("unexpected delay %s", d)
	}
}

type resultStore struct {
	dead, retried bool
	status        *int
}

func (s *resultStore) Create(context.Context, string, domain.CreateRequest, []byte) (domain.Notification, bool, error) {
	return domain.Notification{}, false, nil
}
func (s *resultStore) Get(context.Context, string) (domain.Notification, error) {
	return domain.Notification{}, nil
}
func (s *resultStore) Claim(context.Context, int, time.Duration) ([]domain.Notification, error) {
	return nil, nil
}
func (s *resultStore) MarkDelivered(context.Context, string, int, int) error { return nil }
func (s *resultStore) ScheduleRetry(context.Context, string, int, time.Time, string, *int) error {
	s.retried = true
	return nil
}
func (s *resultStore) MarkDead(_ context.Context, _ string, _ int, _ string, status *int) error {
	s.dead, s.status = true, status
	return nil
}
func (s *resultStore) Counts(context.Context) (map[domain.Status]float64, error) {
	return map[domain.Status]float64{}, nil
}
func (s *resultStore) Ping(context.Context) error { return nil }
func (s *resultStore) Close()                     {}

func TestMaxAttemptsTransitionsToDead(t *testing.T) {
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer vendor.Close()
	st := &resultStore{}
	policy := target.Policy{AllowHTTP: true, AllowPrivate: true}
	metrics := observability.New(st)
	w := NewWorker(st, NewClient(policy, time.Second), metrics, slog.New(slog.NewTextHandler(io.Discard, nil)), 1, 3, time.Millisecond, time.Second, time.Millisecond, time.Second)
	w.process(context.Background(), domain.Notification{ID: "n-1", URL: vendor.URL, Method: "POST", Body: []byte(`{}`), AttemptCount: 3})
	if !st.dead || st.retried || st.status == nil || *st.status != 500 {
		t.Fatalf("unexpected outcome: %+v", st)
	}
}
