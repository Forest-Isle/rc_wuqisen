package delivery

import (
	"context"
	"fmt"
	"github.com/Forest-Isle/rc_wuqisen/internal/domain"
	"github.com/Forest-Isle/rc_wuqisen/internal/target"
	"io"
	"net/http"
	"time"
)

type Result struct {
	Status     int
	Retryable  bool
	RetryAfter time.Duration
	Error      string
	Duration   time.Duration
}
type Client struct {
	http    *http.Client
	policy  target.Policy
	timeout time.Duration
}

func NewClient(p target.Policy, timeout time.Duration) *Client {
	tr := &http.Transport{Proxy: nil, DialContext: p.DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: timeout}
	return &Client{http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, policy: p, timeout: timeout}
}
func (c *Client) Deliver(ctx context.Context, n domain.Notification) Result {
	start := time.Now()
	if _, e := c.policy.Validate(n.URL); e != nil {
		return Result{Retryable: false, Error: "target policy rejected request", Duration: time.Since(start)}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, n.Method, n.URL, bytesReader(n.Body))
	if e != nil {
		return Result{Retryable: false, Error: "invalid outbound request", Duration: time.Since(start)}
	}
	for k, v := range n.Headers {
		if !IsHopByHop(k) && k != "X-Notification-Id" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("X-Notification-ID", n.ID)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return Result{Retryable: true, Error: "network request failed", Duration: time.Since(start)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	res := Result{Status: resp.StatusCode, Duration: time.Since(start)}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return res
	}
	res.Retryable = RetryableStatus(resp.StatusCode)
	res.Error = fmt.Sprintf("supplier returned HTTP %d", resp.StatusCode)
	res.RetryAfter = RetryAfter(resp.Header, time.Now())
	return res
}

type reader struct {
	b []byte
	i int
}

func bytesReader(b []byte) *reader { return &reader{b: b} }
func (r *reader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
