-- +goose Up

CREATE TABLE IF NOT EXISTS teams_feedback_correlation (
    conversation_id TEXT   NOT NULL,
    message_id      TEXT   NOT NULL,
    task_id         UUID   NOT NULL,
    session_id      UUID   NOT NULL,
    created_at      BIGINT NOT NULL,
    PRIMARY KEY (conversation_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_teams_feedback_correlation_created_at
    ON teams_feedback_correlation (created_at);

-- +goose Down

DROP TABLE IF EXISTS teams_feedback_correlation;
