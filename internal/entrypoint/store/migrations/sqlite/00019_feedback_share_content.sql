-- +goose Up

ALTER TABLE feedback ADD COLUMN share_content INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_feedback_share_content ON feedback(task_id, share_content);

-- +goose Down

DROP INDEX IF EXISTS idx_feedback_share_content;
