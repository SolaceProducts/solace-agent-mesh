-- +goose Up

ALTER TABLE task_checkpoints ADD COLUMN renewed_at BIGINT;
ALTER TABLE task_checkpoints ADD COLUMN final_message TEXT;

-- +goose Down

ALTER TABLE task_checkpoints DROP COLUMN final_message;
ALTER TABLE task_checkpoints DROP COLUMN renewed_at;
