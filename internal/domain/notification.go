package domain

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type Status string

const (
	Pending    Status = "pending"
	Processing Status = "processing"
	Delivered  Status = "delivered"
	Dead       Status = "dead"
)

type CreateRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}
type Notification struct {
	ID                   string
	IdempotencyKey       string
	RequestHash          []byte
	URL, Method          string
	Headers              map[string]string
	Body                 []byte
	Status               Status
	AttemptCount         int
	NextAttemptAt        time.Time
	LeaseUntil           *time.Time
	LastError            *string
	ResponseStatus       *int
	CreatedAt, UpdatedAt time.Time
	DeliveredAt          *time.Time
}
type PublicStatus struct {
	ID             string     `json:"id"`
	Status         Status     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	LeaseUntil     *time.Time `json:"lease_until,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
	ResponseStatus *int       `json:"response_status,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

func (n Notification) Public() PublicStatus {
	return PublicStatus{
		ID: n.ID, Status: n.Status, AttemptCount: n.AttemptCount,
		NextAttemptAt: &n.NextAttemptAt, LeaseUntil: n.LeaseUntil,
		LastError: n.LastError, ResponseStatus: n.ResponseStatus,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, DeliveredAt: n.DeliveredAt,
	}
}
func CanonicalHash(r CreateRequest) ([]byte, error) {
	m := strings.ToUpper(strings.TrimSpace(r.Method))
	hs := map[string]string{}
	for k, v := range r.Headers {
		hs[http.CanonicalHeaderKey(strings.TrimSpace(k))] = v
	}
	var body any
	if err := json.Unmarshal(r.Body, &body); err != nil {
		return nil, err
	}
	b, err := json.Marshal(struct {
		URL, Method string
		Headers     map[string]string
		Body        any
	}{r.URL, m, hs, body})
	if err != nil {
		return nil, err
	}
	s := sha256.Sum256(b)
	return s[:], nil
}
