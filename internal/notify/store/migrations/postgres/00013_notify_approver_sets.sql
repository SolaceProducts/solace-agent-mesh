-- +goose Up

ALTER TABLE notify_pending_requests ADD COLUMN IF NOT EXISTS approvers TEXT;
ALTER TABLE notify_pending_requests ADD COLUMN IF NOT EXISTS answered_by TEXT;
ALTER TABLE notify_pending_requests ADD COLUMN IF NOT EXISTS answered_via TEXT;
ALTER TABLE notify_pending_requests ADD COLUMN IF NOT EXISTS decision TEXT;
ALTER TABLE notify_pending_requests ADD COLUMN IF NOT EXISTS answered_by_name TEXT;

CREATE TABLE IF NOT EXISTS notify_hil_cards (
    request_id   TEXT   NOT NULL,
    channel_type TEXT   NOT NULL,
    card         TEXT   NOT NULL,
    created_at   BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notify_hil_cards_request ON notify_hil_cards (request_id);

-- +goose Down
DROP INDEX IF EXISTS idx_notify_hil_cards_request;
DROP TABLE IF EXISTS notify_hil_cards;
ALTER TABLE notify_pending_requests DROP COLUMN IF EXISTS answered_by_name;
ALTER TABLE notify_pending_requests DROP COLUMN IF EXISTS decision;
ALTER TABLE notify_pending_requests DROP COLUMN IF EXISTS answered_via;
ALTER TABLE notify_pending_requests DROP COLUMN IF EXISTS answered_by;
ALTER TABLE notify_pending_requests DROP COLUMN IF EXISTS approvers;
