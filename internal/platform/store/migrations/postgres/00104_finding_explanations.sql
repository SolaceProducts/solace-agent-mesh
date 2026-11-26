
-- +goose Up
CREATE TABLE IF NOT EXISTS finding_explanations (
    finding_id         TEXT NOT NULL,
    explainer_version  TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('explained', 'refused', 'failed')),
    reason_code        TEXT,
    input_digest       TEXT,
    what_happens       TEXT NOT NULL,
    likely_cause       TEXT NOT NULL,
    next_step          TEXT NOT NULL,
    model              TEXT NOT NULL,
    traces_read        INTEGER NOT NULL,
    explained_time     BIGINT,
    called_time        BIGINT NOT NULL,
    checked_time       BIGINT NOT NULL,
    created_time       BIGINT NOT NULL,
    updated_time       BIGINT NOT NULL,
    PRIMARY KEY (finding_id, explainer_version),
    CHECK ((explained_time IS NULL) = (input_digest IS NULL)),
    CHECK (status <> 'explained' OR (explained_time IS NOT NULL AND reason_code IS NULL)),
    CHECK (status = 'explained' OR reason_code IS NOT NULL),
    CHECK (reason_code <> '' AND input_digest <> ''),
    CHECK (explained_time IS NULL OR (what_happens <> '' AND likely_cause <> '' AND next_step <> ''))
);

CREATE INDEX IF NOT EXISTS ix_finding_explanations_checked ON finding_explanations(checked_time);

CREATE TABLE IF NOT EXISTS finding_explanation_calls (
    finding_id   TEXT NOT NULL,
    called_time  BIGINT NOT NULL,
    PRIMARY KEY (finding_id, called_time)
);

CREATE INDEX IF NOT EXISTS ix_finding_explanation_calls_called ON finding_explanation_calls(called_time);

-- +goose Down
DROP INDEX IF EXISTS ix_finding_explanation_calls_called;
DROP TABLE IF EXISTS finding_explanation_calls;
DROP INDEX IF EXISTS ix_finding_explanations_checked;
DROP TABLE IF EXISTS finding_explanations;
