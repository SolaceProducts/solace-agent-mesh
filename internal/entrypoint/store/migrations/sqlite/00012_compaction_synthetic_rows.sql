-- +goose Up

ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'task'
    CHECK (kind IN ('task', 'compaction-cost'));
ALTER TABLE tasks ADD COLUMN post_compaction_remaining_tokens INTEGER;
CREATE INDEX IF NOT EXISTS idx_tasks_kind ON tasks(kind);

ALTER TABLE chat_tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'task'
    CHECK (kind IN ('task', 'compaction_notification'));
CREATE INDEX IF NOT EXISTS idx_chat_tasks_kind ON chat_tasks(kind);

-- +goose Down

DROP INDEX IF EXISTS idx_chat_tasks_kind;
DROP INDEX IF EXISTS idx_tasks_kind;
