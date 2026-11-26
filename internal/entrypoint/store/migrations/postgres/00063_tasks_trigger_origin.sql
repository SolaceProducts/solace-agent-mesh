-- +goose Up

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS channel TEXT,
    ADD COLUMN IF NOT EXISTS entrypoint_id TEXT,
    ADD COLUMN IF NOT EXISTS entrypoint_name TEXT,
    ADD COLUMN IF NOT EXISTS trigger_source TEXT;

-- +goose Down

ALTER TABLE tasks
    DROP COLUMN IF EXISTS trigger_source,
    DROP COLUMN IF EXISTS entrypoint_name,
    DROP COLUMN IF EXISTS entrypoint_id,
    DROP COLUMN IF EXISTS channel;
