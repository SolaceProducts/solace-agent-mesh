-- +goose Up

UPDATE shared_link_users SET access_level = LOWER(access_level)
    WHERE access_level <> LOWER(access_level);
UPDATE shared_link_users SET original_access_level = LOWER(original_access_level)
    WHERE original_access_level IS NOT NULL
      AND original_access_level <> LOWER(original_access_level);
UPDATE resource_access_shares SET access_level = LOWER(access_level)
    WHERE access_level <> LOWER(access_level);

CREATE TABLE shared_link_users_new (
    id TEXT PRIMARY KEY,
    share_id TEXT NOT NULL REFERENCES shared_links(share_id) ON DELETE CASCADE,
    user_email TEXT NOT NULL,
    access_level TEXT NOT NULL DEFAULT 'resource_viewer',
    added_at INTEGER NOT NULL,
    added_by_user_id TEXT NOT NULL,
    original_access_level TEXT,
    original_added_at INTEGER,
    UNIQUE(share_id, user_email)
);
INSERT INTO shared_link_users_new
    (id, share_id, user_email, access_level, added_at, added_by_user_id, original_access_level, original_added_at)
SELECT id, share_id, user_email, access_level, added_at, added_by_user_id, original_access_level, original_added_at
    FROM shared_link_users;
DROP TABLE shared_link_users;
ALTER TABLE shared_link_users_new RENAME TO shared_link_users;
CREATE INDEX IF NOT EXISTS idx_shared_link_users_share_id ON shared_link_users(share_id);
CREATE INDEX IF NOT EXISTS idx_shared_link_users_user_email ON shared_link_users(user_email);

-- +goose Down

UPDATE shared_link_users SET access_level = UPPER(access_level)
    WHERE access_level <> UPPER(access_level);
UPDATE shared_link_users SET original_access_level = UPPER(original_access_level)
    WHERE original_access_level IS NOT NULL
      AND original_access_level <> UPPER(original_access_level);
UPDATE resource_access_shares SET access_level = UPPER(access_level)
    WHERE access_level <> UPPER(access_level);
