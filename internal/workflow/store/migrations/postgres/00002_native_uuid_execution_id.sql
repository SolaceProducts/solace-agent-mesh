-- +goose Up

ALTER TABLE workflow_paused ALTER COLUMN execution_id TYPE uuid USING execution_id::uuid;

-- +goose Down

ALTER TABLE workflow_paused ALTER COLUMN execution_id TYPE text USING execution_id::text;
