-- +goose NO TRANSACTION
-- +goose Up

-- +goose Down

DROP INDEX CONCURRENTLY IF EXISTS idx_task_events_config_tools_blob;
DROP INDEX CONCURRENTLY IF EXISTS idx_task_events_config_prompt_blob;
