
-- +goose Up
CREATE TABLE IF NOT EXISTS budget_alert_state (
    subject_type  TEXT   NOT NULL,
    subject_ref   TEXT   NOT NULL,
    period_start  BIGINT NOT NULL,
    alerted_level TEXT   NOT NULL,
    cap_key       TEXT   NOT NULL,
    updated_time  BIGINT NOT NULL,
    PRIMARY KEY (subject_type, subject_ref, period_start),
    CONSTRAINT ck_budget_alert_state_subject_type CHECK (subject_type IN ('mesh', 'user')),
    CONSTRAINT ck_budget_alert_state_level CHECK (alerted_level IN ('none', 'warn', 'limit'))
);

CREATE INDEX IF NOT EXISTS idx_budget_alert_state_period_start
    ON budget_alert_state (period_start);

-- +goose Down
DROP TABLE IF EXISTS budget_alert_state;
