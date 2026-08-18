package store

import (
	"context"
	"errors"
	"github.com/wuqisen/reliable-notification-service/internal/domain"
	"time"
)

var ErrNotFound = errors.New("notification not found")
var ErrIdempotencyConflict = errors.New("idempotency key reused with different request")
var ErrLeaseLost = errors.New("notification lease is no longer current")

type Store interface {
	Create(context.Context, string, domain.CreateRequest, []byte) (domain.Notification, bool, error)
	Get(context.Context, string) (domain.Notification, error)
	Claim(context.Context, int, time.Duration) ([]domain.Notification, error)
	MarkDelivered(context.Context, string, int, int) error
	ScheduleRetry(context.Context, string, int, time.Time, string, *int) error
	MarkDead(context.Context, string, int, string, *int) error
	Counts(context.Context) (map[domain.Status]float64, error)
	Ping(context.Context) error
	Close()
}
