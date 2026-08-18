package delivery

import (
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func RetryableStatus(code int) bool {
	return code == 408 || code == 425 || code == 429 || code >= 500 && code <= 599
}
func Delay(attempt int, initial, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	n := float64(initial) * math.Pow(2, float64(attempt-1))
	if n > float64(max) {
		n = float64(max)
	}
	return time.Duration(n * (1 + rand.Float64()*.25))
}
func RetryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, e := strconv.Atoi(v); e == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(v); e == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
func IsHopByHop(k string) bool {
	switch strings.ToLower(k) {
	case "host", "content-length", "connection", "transfer-encoding", "upgrade", "proxy-authorization", "proxy-authenticate", "te", "trailer":
		return true
	}
	return false
}
