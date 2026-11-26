-- +goose Up

ALTER TABLE shared_link_users ADD COLUMN viewed_at INTEGER;
CREATE INDEX IF NOT EXISTS idx_shared_link_users_email_added ON shared_link_users(user_email, added_at DESC);

DROP INDEX IF EXISTS idx_shared_link_users_user_email;

UPDATE shared_link_users SET viewed_at = added_at WHERE viewed_at IS NULL;

-- +goose Down

CREATE INDEX IF NOT EXISTS idx_shared_link_users_user_email ON shared_link_users(user_email);
DROP INDEX IF EXISTS idx_shared_link_users_email_added;
