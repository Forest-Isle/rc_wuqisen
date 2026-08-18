package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wuqisen/reliable-notification-service/internal/domain"
	"github.com/wuqisen/reliable-notification-service/internal/observability"
	"github.com/wuqisen/reliable-notification-service/internal/store"
	"github.com/wuqisen/reliable-notification-service/internal/target"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
)

type Server struct {
	store   store.Store
	policy  target.Policy
	token   string
	maxBody int64
	metrics *observability.Metrics
	log     *slog.Logger
	handler http.Handler
}

func New(st store.Store, p target.Policy, token string, maxBody int64, m *observability.Metrics, l *slog.Logger) *Server {
	s := &Server{store: st, policy: p, token: token, maxBody: maxBody, metrics: m, log: l}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			method(w, "GET")
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", s.ready)
	mux.Handle("/metrics", m.Handler())
	mux.HandleFunc("/v1/notifications", s.auth(s.create))
	mux.HandleFunc("/v1/notifications/", s.auth(s.get))
	s.handler = s.wrap(mux)
	return s
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID()
		w.Header().Set("X-Request-ID", id)
		defer func() {
			if x := recover(); x != nil {
				s.log.Error("panic recovered", "request_id", id)
				problem(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		v := ""
		if strings.HasPrefix(auth, "Bearer ") {
			v = strings.TrimPrefix(auth, "Bearer ")
		}
		if len(v) != len(s.token) || subtle.ConstantTimeCompare([]byte(v), []byte(s.token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, 401, "unauthorized", "valid bearer token required")
			return
		}
		next(w, r)
	}
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		method(w, "GET")
		return
	}
	ctx := r.Context()
	if e := s.store.Ping(ctx); e != nil {
		problem(w, 503, "not_ready", "database unavailable")
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		method(w, "POST")
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		problem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var req domain.CreateRequest
	if e := d.Decode(&req); e != nil {
		if strings.Contains(e.Error(), "request body too large") {
			problem(w, 413, "request_too_large", "request exceeds configured limit")
		} else {
			problem(w, 400, "invalid_json", "invalid request body")
		}
		return
	}
	if e := d.Decode(&struct{}{}); e != io.EOF {
		problem(w, 400, "invalid_json", "request must contain one JSON object")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validKey(key) {
		problem(w, 400, "invalid_idempotency_key", "Idempotency-Key must be 1-128 printable ASCII characters")
		return
	}
	if e := validateRequest(&req, s.policy); e != nil {
		problem(w, 422, "invalid_notification", e.Error())
		return
	}
	hash, e := domain.CanonicalHash(req)
	if e != nil {
		problem(w, 400, "invalid_body", "body must be valid JSON")
		return
	}
	n, replayed, e := s.store.Create(r.Context(), key, req, hash)
	if errors.Is(e, store.ErrIdempotencyConflict) {
		s.metrics.Accepted.WithLabelValues("conflict").Inc()
		problem(w, 409, "idempotency_conflict", "key was already used for another request")
		return
	}
	if e != nil {
		s.log.Error("persist notification", "error", e)
		problem(w, 500, "internal_error", "could not persist notification")
		return
	}
	out := n.Public()
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		s.metrics.Accepted.WithLabelValues("replayed").Inc()
	} else {
		s.metrics.Accepted.WithLabelValues("accepted").Inc()
	}
	write(w, 202, out)
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		method(w, "GET")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/notifications/")
	if !uuidRE.MatchString(id) {
		problem(w, 404, "not_found", "notification not found")
		return
	}
	n, e := s.store.Get(r.Context(), id)
	if errors.Is(e, store.ErrNotFound) {
		problem(w, 404, "not_found", "notification not found")
		return
	}
	if e != nil {
		problem(w, 500, "internal_error", "could not load notification")
		return
	}
	write(w, 200, n.Public())
}
func validateRequest(r *domain.CreateRequest, p target.Policy) error {
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	if r.Method != "POST" && r.Method != "PUT" && r.Method != "PATCH" {
		return fmt.Errorf("method must be POST, PUT, or PATCH")
	}
	if len(r.URL) > 2048 {
		return fmt.Errorf("url is too long")
	}
	u, e := p.Validate(r.URL)
	if e != nil {
		return e
	}
	r.URL = u.String()
	if r.Body == nil {
		return fmt.Errorf("body is required")
	}
	if len(r.Headers) > 32 {
		return fmt.Errorf("too many headers")
	}
	clean := map[string]string{}
	for k, v := range r.Headers {
		k = strings.TrimSpace(k)
		if len(k) > 128 || len(v) > 4096 || !validHeaderName(k) || !validHeaderValue(v) {
			return fmt.Errorf("invalid or forbidden header")
		}
		k = http.CanonicalHeaderKey(k)
		if deliveryHeaderForbidden(k) {
			return fmt.Errorf("invalid or forbidden header")
		}
		clean[k] = v
	}
	r.Headers = clean
	return nil
}
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}
func validHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			continue
		}
		if s[i] < 32 || s[i] == 127 {
			return false
		}
	}
	return true
}
func deliveryHeaderForbidden(k string) bool {
	return strings.EqualFold(k, "X-Notification-ID") || map[string]bool{"Host": true, "Content-Length": true, "Connection": true, "Transfer-Encoding": true, "Upgrade": true, "Proxy-Authorization": true, "Proxy-Authenticate": true, "Te": true, "Trailer": true}[http.CanonicalHeaderKey(k)]
}
func validKey(k string) bool {
	if len(k) < 1 || len(k) > 128 {
		return false
	}
	for _, c := range []byte(k) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func requestID() string { b := make([]byte, 12); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func method(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	problem(w, 405, "method_not_allowed", "method not allowed")
}
func problem(w http.ResponseWriter, status int, code, msg string) {
	writeStatus(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
func write(w http.ResponseWriter, status int, v any) { writeStatus(w, status, v) }
func writeStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
