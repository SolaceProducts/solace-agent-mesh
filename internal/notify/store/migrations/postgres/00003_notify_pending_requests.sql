-- +goose Up

CREATE TABLE IF NOT EXISTS notify_pending_requests (
    request_id        TEXT PRIMARY KEY,
    requester_addr    TEXT NOT NULL,
    recipient_user_id TEXT NOT NULL DEFAULT '',
    dedup             TEXT,
    status            TEXT NOT NULL DEFAULT 'pending',
    expires_at        BIGINT,
    created_at        BIGINT NOT NULL,
    resolved_at       BIGINT
);

CREATE INDEX IF NOT EXISTS idx_notify_pending_status_expires
    ON notify_pending_requests (status, expires_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notify_pending_dedup
    ON notify_pending_requests (dedup) WHERE dedup IS NOT NULL;

-- +goose Down
DROP TABLE notify_pending_requests;
