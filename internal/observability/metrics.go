package observability

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/wuqisen/reliable-notification-service/internal/domain"
	"net/http"
	"time"
)

type CounterStore interface {
	Counts(context.Context) (map[domain.Status]float64, error)
}
type Metrics struct {
	Accepted *prometheus.CounterVec
	Attempts *prometheus.CounterVec
	Duration prometheus.Histogram
	reg      *prometheus.Registry
}

func New(s CounterStore) *Metrics {
	r := prometheus.NewRegistry()
	m := &Metrics{Accepted: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "notifications_accepted_total", Help: "Notification API outcomes."}, []string{"outcome"}), Attempts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "notification_delivery_attempts_total", Help: "Delivery attempt outcomes."}, []string{"outcome"}), Duration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "notification_delivery_duration_seconds", Help: "Supplier request duration.", Buckets: prometheus.DefBuckets}), reg: r}
	r.MustRegister(m.Accepted, m.Attempts, m.Duration)
	for _, st := range []domain.Status{domain.Pending, domain.Processing, domain.Delivered, domain.Dead} {
		status := st
		r.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "notifications_queue", Help: "Current notifications by status.", ConstLabels: prometheus.Labels{"status": string(status)}}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			v, e := s.Counts(ctx)
			if e != nil {
				return 0
			}
			return v[status]
		}))
	}
	return m
}
func (m *Metrics) Handler() http.Handler { return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}) }
