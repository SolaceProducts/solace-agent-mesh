
-- +goose Up
ALTER TABLE platform_webui_settings ADD COLUMN feedback_task_info_level TEXT;

-- +goose Down
ALTER TABLE platform_webui_settings DROP COLUMN feedback_task_info_level;
