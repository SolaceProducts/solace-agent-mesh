
-- +goose Up
-- +goose StatementBegin
CREATE TABLE finding_eval_sets_new (
    finding_id       TEXT PRIMARY KEY,
    problem_key      TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('drafting', 'drafted', 'declined', 'refused', 'failed')),
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
    attempted_time   INTEGER NOT NULL,
    created_time     INTEGER NOT NULL,
    updated_time     INTEGER NOT NULL,
    CHECK ((status = 'drafted') = (dataset_id IS NOT NULL AND evaluator_id IS NOT NULL AND experiment_id IS NOT NULL)),
    CHECK ((status IN ('declined', 'refused', 'failed')) = (reason_code IS NOT NULL)),
    CHECK (length(reason_code) > 0),
    CHECK (attempted_time > 0 OR (status = 'failed' AND reason_code = 'scan_ended'))
);
INSERT INTO finding_eval_sets_new SELECT finding_id, problem_key, status, reason_code, agent_name, drafter_version,
    model, criterion, example_count, dataset_id, evaluator_id, experiment_id, attempts, attempted_time, created_time,
    updated_time FROM finding_eval_sets;
DROP INDEX IF EXISTS ix_finding_eval_sets_problem;
DROP INDEX IF EXISTS ix_finding_eval_sets_attempted;
DROP TABLE finding_eval_sets;
ALTER TABLE finding_eval_sets_new RENAME TO finding_eval_sets;
CREATE INDEX ix_finding_eval_sets_attempted ON finding_eval_sets(attempted_time);
CREATE INDEX ix_finding_eval_sets_problem ON finding_eval_sets(problem_key) WHERE status = 'drafted';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE finding_eval_sets_old (
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
    attempted_time   INTEGER NOT NULL,
    created_time     INTEGER NOT NULL,
    updated_time     INTEGER NOT NULL,
    CHECK ((status = 'drafted') = (dataset_id IS NOT NULL AND evaluator_id IS NOT NULL AND experiment_id IS NOT NULL)),
    CHECK ((status IN ('refused', 'failed')) = (reason_code IS NOT NULL)),
    CHECK (length(reason_code) > 0),
    CHECK (attempted_time > 0 OR (status = 'failed' AND reason_code = 'scan_ended'))
);
INSERT INTO finding_eval_sets_old SELECT finding_id, problem_key, status, reason_code, agent_name, drafter_version,
    model, criterion, example_count, dataset_id, evaluator_id, experiment_id, attempts, attempted_time, created_time,
    updated_time FROM finding_eval_sets WHERE status <> 'declined';
DROP INDEX IF EXISTS ix_finding_eval_sets_problem;
DROP INDEX IF EXISTS ix_finding_eval_sets_attempted;
DROP TABLE finding_eval_sets;
ALTER TABLE finding_eval_sets_old RENAME TO finding_eval_sets;
CREATE INDEX ix_finding_eval_sets_attempted ON finding_eval_sets(attempted_time);
CREATE INDEX ix_finding_eval_sets_problem ON finding_eval_sets(problem_key) WHERE status = 'drafted';
-- +goose StatementEnd
