
-- +goose Up
ALTER TABLE platform_webui_settings ADD COLUMN publish_feedback INTEGER;

-- +goose Down
ALTER TABLE platform_webui_settings DROP COLUMN publish_feedback;
