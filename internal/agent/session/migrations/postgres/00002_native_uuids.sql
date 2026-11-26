-- +goose Up

ALTER TABLE task_checkpoints ALTER COLUMN task_id TYPE UUID USING task_id::uuid;

-- +goose Down

ALTER TABLE task_checkpoints ALTER COLUMN task_id TYPE TEXT USING task_id::text;
