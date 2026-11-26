
-- +goose Up
CREATE TABLE IF NOT EXISTS eval_generation_operations (
    id TEXT PRIMARY KEY,
    operation_type TEXT NOT NULL,
    dataset_id TEXT REFERENCES eval_datasets(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    error_message TEXT,
    error_id TEXT,
    created_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    completed_time BIGINT,
    deadline BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS ix_eval_generation_operations_dataset
    ON eval_generation_operations (dataset_id, created_time);

CREATE UNIQUE INDEX IF NOT EXISTS uq_eval_generation_operations_examples_in_progress
    ON eval_generation_operations (dataset_id)
    WHERE status = 'inProgress' AND operation_type = 'generate_examples';

CREATE INDEX IF NOT EXISTS ix_eval_generation_operations_in_progress
    ON eval_generation_operations (deadline)
    WHERE status = 'inProgress';

-- +goose Down
DROP TABLE IF EXISTS eval_generation_operations;
