
-- +goose Up
CREATE INDEX IF NOT EXISTS idx_users_provisioned_via ON users (provisioned_via);

-- +goose Down
DROP INDEX IF EXISTS idx_users_provisioned_via;
