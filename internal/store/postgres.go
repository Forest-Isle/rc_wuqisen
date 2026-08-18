package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wuqisen/reliable-notification-service/internal/domain"
	"time"
)

type Postgres struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Postgres, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Postgres{p}, nil
}
func (p *Postgres) Pool() *pgxpool.Pool          { return p.pool }
func (p *Postgres) Ping(c context.Context) error { return p.pool.Ping(c) }
func (p *Postgres) Close()                       { p.pool.Close() }
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:])
}
func (p *Postgres) Create(ctx context.Context, key string, r domain.CreateRequest, hash []byte) (domain.Notification, bool, error) {
	h, _ := json.Marshal(r.Headers)
	id := newID()
	row := p.pool.QueryRow(ctx, `INSERT INTO notifications(id,idempotency_key,request_hash,target_url,method,headers,body,status,next_attempt_at) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',now()) ON CONFLICT(idempotency_key) DO NOTHING RETURNING id,idempotency_key,request_hash,target_url,method,headers,body,status,attempt_count,next_attempt_at,lease_until,last_error,response_status,created_at,updated_at,delivered_at`, id, key, hash, r.URL, r.Method, h, []byte(r.Body))
	n, e := scan(row)
	if e == nil {
		return n, false, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return n, false, e
	}
	n, e = p.byKey(ctx, key)
	if e != nil {
		return n, false, e
	}
	if !bytes.Equal(n.RequestHash, hash) {
		return n, false, ErrIdempotencyConflict
	}
	return n, true, nil
}
func (p *Postgres) byKey(ctx context.Context, key string) (domain.Notification, error) {
	return scan(p.pool.QueryRow(ctx, selectCols+` WHERE idempotency_key=$1`, key))
}
func (p *Postgres) Get(ctx context.Context, id string) (domain.Notification, error) {
	n, e := scan(p.pool.QueryRow(ctx, selectCols+` WHERE id=$1`, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, e
}

const selectCols = `SELECT id,idempotency_key,request_hash,target_url,method,headers,body,status,attempt_count,next_attempt_at,lease_until,last_error,response_status,created_at,updated_at,delivered_at FROM notifications`

type scanner interface{ Scan(...any) error }

func scan(s scanner) (domain.Notification, error) {
	var n domain.Notification
	var h []byte
	e := s.Scan(&n.ID, &n.IdempotencyKey, &n.RequestHash, &n.URL, &n.Method, &h, &n.Body, &n.Status, &n.AttemptCount, &n.NextAttemptAt, &n.LeaseUntil, &n.LastError, &n.ResponseStatus, &n.CreatedAt, &n.UpdatedAt, &n.DeliveredAt)
	if e == nil {
		e = json.Unmarshal(h, &n.Headers)
	}
	return n, e
}
func (p *Postgres) Claim(ctx context.Context, limit int, lease time.Duration) ([]domain.Notification, error) {
	rows, e := p.pool.Query(ctx, `WITH due AS (SELECT id FROM notifications WHERE (status='pending' AND next_attempt_at<=now()) OR (status='processing' AND lease_until<=now()) ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE notifications n SET status='processing',attempt_count=attempt_count+1,lease_until=now()+$2::interval,updated_at=now() FROM due WHERE n.id=due.id RETURNING n.id,n.idempotency_key,n.request_hash,n.target_url,n.method,n.headers,n.body,n.status,n.attempt_count,n.next_attempt_at,n.lease_until,n.last_error,n.response_status,n.created_at,n.updated_at,n.delivered_at`, limit, lease.String())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.Notification
	for rows.Next() {
		n, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (p *Postgres) MarkDelivered(c context.Context, id string, attempt, code int) error {
	r, e := p.pool.Exec(c, `UPDATE notifications SET status='delivered',response_status=$3,last_error=NULL,lease_until=NULL,delivered_at=now(),updated_at=now() WHERE id=$1 AND status='processing' AND attempt_count=$2`, id, attempt, code)
	return affected(r.RowsAffected(), e)
}
func (p *Postgres) ScheduleRetry(c context.Context, id string, attempt int, next time.Time, msg string, code *int) error {
	r, e := p.pool.Exec(c, `UPDATE notifications SET status='pending',next_attempt_at=$3,last_error=$4,response_status=$5,lease_until=NULL,updated_at=now() WHERE id=$1 AND status='processing' AND attempt_count=$2`, id, attempt, next, msg, code)
	return affected(r.RowsAffected(), e)
}
func (p *Postgres) MarkDead(c context.Context, id string, attempt int, msg string, code *int) error {
	r, e := p.pool.Exec(c, `UPDATE notifications SET status='dead',last_error=$3,response_status=$4,lease_until=NULL,updated_at=now() WHERE id=$1 AND status='processing' AND attempt_count=$2`, id, attempt, msg, code)
	return affected(r.RowsAffected(), e)
}
func affected(n int64, e error) error {
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (p *Postgres) Counts(c context.Context) (map[domain.Status]float64, error) {
	rows, e := p.pool.Query(c, `SELECT status,count(*) FROM notifications GROUP BY status`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	m := map[domain.Status]float64{}
	for rows.Next() {
		var s domain.Status
		var n float64
		if e = rows.Scan(&s, &n); e != nil {
			return nil, e
		}
		m[s] = n
	}
	return m, rows.Err()
}
