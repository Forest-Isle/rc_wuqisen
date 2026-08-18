package api

import (
	"context"
	"encoding/json"
	"github.com/wuqisen/reliable-notification-service/internal/domain"
	"github.com/wuqisen/reliable-notification-service/internal/observability"
	"github.com/wuqisen/reliable-notification-service/internal/store"
	"github.com/wuqisen/reliable-notification-service/internal/target"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	n           domain.Notification
	createCalls int
}

func (f *fakeStore) Create(_ context.Context, key string, r domain.CreateRequest, h []byte) (domain.Notification, bool, error) {
	f.createCalls++
	if f.n.ID != "" {
		if f.n.IdempotencyKey == key {
			if string(f.n.RequestHash) == string(h) {
				return f.n, true, nil
			}
			return f.n, false, store.ErrIdempotencyConflict
		}
	}
	f.n = domain.Notification{ID: "550e8400-e29b-41d4-a716-446655440000", IdempotencyKey: key, RequestHash: h, URL: r.URL, Method: r.Method, Headers: r.Headers, Body: r.Body, Status: domain.Pending, NextAttemptAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	return f.n, false, nil
}
func (f *fakeStore) Get(context.Context, string) (domain.Notification, error) {
	if f.n.ID == "" {
		return f.n, store.ErrNotFound
	}
	return f.n, nil
}
func (f *fakeStore) Claim(context.Context, int, time.Duration) ([]domain.Notification, error) {
	return nil, nil
}
func (f *fakeStore) MarkDelivered(context.Context, string, int, int) error { return nil }
func (f *fakeStore) ScheduleRetry(context.Context, string, int, time.Time, string, *int) error {
	return nil
}
func (f *fakeStore) MarkDead(context.Context, string, int, string, *int) error { return nil }
func (f *fakeStore) Counts(context.Context) (map[domain.Status]float64, error) { return nil, nil }
func (f *fakeStore) Ping(context.Context) error                                { return nil }
func (f *fakeStore) Close()                                                    {}
func testServer(f *fakeStore) *Server {
	return New(f, target.Policy{AllowHTTP: true, AllowPrivate: true}, "token", 10000, observability.New(f), slog.New(slog.NewTextHandler(ioDiscard{}, nil)))
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
func req(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer token")
	r.Header.Set("Idempotency-Key", "k-1")
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestCreateAuthAndIdempotency(t *testing.T) {
	f := &fakeStore{}
	h := testServer(f).Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req("POST", "/v1/notifications", `{"url":"http://127.0.0.1:9/x","method":"POST","body":{"a":1}}`))
	if rr.Code != 202 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req("POST", "/v1/notifications", `{"url":"http://127.0.0.1:9/x","method":"POST","body":{"a":1}}`))
	if rr.Code != 202 || rr.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay got %d %s", rr.Code, rr.Body)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req("POST", "/v1/notifications", `{"url":"http://127.0.0.1:9/x","method":"POST","body":{"a":2}}`))
	if rr.Code != 409 {
		t.Fatalf("conflict got %d", rr.Code)
	}
	bad := httptest.NewRequest("POST", "/v1/notifications", strings.NewReader(`{}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, bad)
	if rr.Code != 401 {
		t.Fatalf("auth got %d", rr.Code)
	}
}
func TestStatusDoesNotExposePayload(t *testing.T) {
	f := &fakeStore{}
	h := testServer(f).Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req("POST", "/v1/notifications", `{"url":"http://127.0.0.1:9/x","method":"POST","headers":{"Authorization":"secret"},"body":{"secret":"value"}}`))
	var v map[string]any
	if e := json.Unmarshal(rr.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	id := v["id"].(string)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req("GET", "/v1/notifications/"+id, ""))
	if strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "value") {
		t.Fatalf("payload leaked: %s", rr.Body)
	}
}

func TestRejectsUnsafeHeaders(t *testing.T) {
	f := &fakeStore{}
	h := testServer(f).Handler()
	for _, body := range []string{
		`{"url":"http://127.0.0.1:9/x","method":"POST","headers":{"X-Bad Name":"x"},"body":{}}`,
		"{\"url\":\"http://127.0.0.1:9/x\",\"method\":\"POST\",\"headers\":{\"X-Test\":\"ok\\r\\nInjected: yes\"},\"body\":{}}",
		`{"url":"http://127.0.0.1:9/x","method":"POST","headers":{"X-Notification-ID":"override"},"body":{}}`,
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req("POST", "/v1/notifications", body))
		if rr.Code != 422 {
			t.Fatalf("got %d for %s: %s", rr.Code, body, rr.Body)
		}
	}
}
