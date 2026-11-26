
-- +goose Up
CREATE TABLE IF NOT EXISTS token_spend_budget_period (
    subject_kind          TEXT   NOT NULL,
    subject_ref           TEXT   NOT NULL,
    period_start          BIGINT NOT NULL,
    period_end            BIGINT NOT NULL,
    priced_calls          BIGINT NOT NULL,
    unpriced_calls        BIGINT NOT NULL,
    uncached_input_tokens BIGINT NOT NULL,
    output_tokens         BIGINT NOT NULL,
    cache_read_tokens     BIGINT NOT NULL,
    cache_write_tokens    BIGINT NOT NULL,
    uncached_input_cost   BIGINT NOT NULL,
    output_cost           BIGINT NOT NULL,
    cache_read_cost       BIGINT NOT NULL,
    cache_write_cost      BIGINT NOT NULL,
    total_cost            BIGINT NOT NULL,
    PRIMARY KEY (subject_kind, subject_ref, period_start),
    CONSTRAINT ck_token_spend_budget_period_subject_kind CHECK (subject_kind IN ('mesh', 'id', 'sub')),
    CONSTRAINT ck_token_spend_budget_period_mesh_ref CHECK (subject_kind <> 'mesh' OR subject_ref = ''),
    CONSTRAINT ck_token_spend_budget_period_window CHECK (period_end > period_start)
);

CREATE INDEX IF NOT EXISTS idx_token_spend_budget_period_start
    ON token_spend_budget_period (period_start);

-- +goose Down
DROP TABLE IF EXISTS token_spend_budget_period;
