-- +goose Up

DROP TABLE IF EXISTS prompt_group_users;

-- +goose Down

CREATE TABLE IF NOT EXISTS prompt_group_users (
    id UUID PRIMARY KEY,
    prompt_group_id UUID NOT NULL REFERENCES prompt_groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    added_at BIGINT NOT NULL,
    added_by_user_id TEXT NOT NULL,
    UNIQUE(prompt_group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_prompt_group_id ON prompt_group_users(prompt_group_id);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_user_id ON prompt_group_users(user_id);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_user_prompt ON prompt_group_users(user_id, prompt_group_id);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_user_role ON prompt_group_users(user_id, role);
