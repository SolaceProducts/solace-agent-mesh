
-- +goose Up
CREATE TABLE IF NOT EXISTS finding_eval_sets (
    finding_id       TEXT PRIMARY KEY,
    problem_key      TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('drafting', 'drafted', 'refused', 'failed')),
    reason_code      TEXT,
    agent_name       TEXT NOT NULL,
    drafter_version  TEXT NOT NULL,
    model            TEXT NOT NULL,
    criterion        TEXT NOT NULL,
    example_count    INTEGER NOT NULL,
    dataset_id       TEXT,
    evaluator_id     TEXT,
    experiment_id    TEXT,
    attempts         INTEGER NOT NULL CHECK (attempts >= 0),
    attempted_time   BIGINT NOT NULL,
    created_time     BIGINT NOT NULL,
    updated_time     BIGINT NOT NULL,
    CHECK ((status = 'drafted') = (dataset_id IS NOT NULL AND evaluator_id IS NOT NULL AND experiment_id IS NOT NULL)),
    CHECK ((status IN ('refused', 'failed')) = (reason_code IS NOT NULL)),
    CHECK (reason_code <> ''),
    CHECK (attempted_time > 0 OR (status = 'failed' AND reason_code = 'scan_ended'))
);

CREATE INDEX IF NOT EXISTS ix_finding_eval_sets_attempted ON finding_eval_sets(attempted_time);

CREATE INDEX IF NOT EXISTS ix_finding_eval_sets_problem ON finding_eval_sets(problem_key) WHERE status = 'drafted';

-- +goose Down
DROP INDEX IF EXISTS ix_finding_eval_sets_problem;
DROP INDEX IF EXISTS ix_finding_eval_sets_attempted;
DROP TABLE IF EXISTS finding_eval_sets;
