-- +goose Up

ALTER TABLE notify_user_channels ADD COLUMN confirmed_address TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_user_channels ADD COLUMN confirmed_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE notify_user_channels ADD COLUMN pending_address TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_user_channels ADD COLUMN pending_code_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_user_channels ADD COLUMN pending_expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE notify_user_channels ADD COLUMN pending_attempts INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE notify_user_channels DROP COLUMN pending_attempts;
ALTER TABLE notify_user_channels DROP COLUMN pending_expires_at;
ALTER TABLE notify_user_channels DROP COLUMN pending_code_hash;
ALTER TABLE notify_user_channels DROP COLUMN pending_address;
ALTER TABLE notify_user_channels DROP COLUMN confirmed_at;
ALTER TABLE notify_user_channels DROP COLUMN confirmed_address;
