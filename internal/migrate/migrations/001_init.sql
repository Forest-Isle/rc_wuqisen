CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_hash BYTEA NOT NULL,
    target_url TEXT NOT NULL,
    method TEXT NOT NULL CHECK (method IN ('POST', 'PUT', 'PATCH')),
    headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    body JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'delivered', 'dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    lease_until TIMESTAMPTZ,
    last_error TEXT,
    response_status INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS notifications_due_idx
    ON notifications (next_attempt_at, id)
    WHERE status IN ('pending', 'processing');
CREATE INDEX IF NOT EXISTS notifications_status_idx ON notifications (status);
