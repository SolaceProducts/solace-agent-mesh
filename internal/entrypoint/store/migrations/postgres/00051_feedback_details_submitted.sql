-- +goose Up

ALTER TABLE feedback ADD COLUMN IF NOT EXISTS details_submitted_time BIGINT;
UPDATE feedback SET details_submitted_time = updated_at
WHERE details_submitted_time IS NULL AND (length(comment) > 0 OR share_content = 1);

-- +goose Down

ALTER TABLE feedback DROP COLUMN IF EXISTS details_submitted_time;
