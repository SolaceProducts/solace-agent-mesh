
-- +goose Up
ALTER TABLE tool_package_files ADD COLUMN auth TEXT;

-- +goose Down
ALTER TABLE tool_package_files DROP COLUMN auth;
