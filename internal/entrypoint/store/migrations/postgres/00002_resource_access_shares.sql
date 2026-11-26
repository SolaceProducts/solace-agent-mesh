-- +goose Up
CREATE TABLE IF NOT EXISTS resource_access_shares (
    id TEXT PRIMARY KEY,
    resource_id TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT 'project',
    shared_with_user_email TEXT NOT NULL,
    access_level TEXT NOT NULL DEFAULT 'resource_viewer',
    shared_by_user_email TEXT NOT NULL,
    shared_at BIGINT NOT NULL,
    updated_at BIGINT,
    UNIQUE(resource_type, resource_id, shared_with_user_email)
);
CREATE INDEX IF NOT EXISTS idx_ras_resource ON resource_access_shares(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_ras_shared_with ON resource_access_shares(shared_with_user_email);
CREATE INDEX IF NOT EXISTS idx_ras_shared_by ON resource_access_shares(shared_by_user_email);

-- +goose Down
DROP TABLE IF EXISTS resource_access_shares;
