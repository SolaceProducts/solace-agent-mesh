-- +goose Up

CREATE INDEX IF NOT EXISTS idx_task_events_config_prompt_blob ON task_events (config_prompt_blob) WHERE config_prompt_blob IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_events_config_tools_blob ON task_events (config_tools_blob) WHERE config_tools_blob IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_task_events_config_tools_blob;
DROP INDEX IF EXISTS idx_task_events_config_prompt_blob;
