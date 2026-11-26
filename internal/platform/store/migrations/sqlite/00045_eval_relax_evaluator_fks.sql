
-- +goose Up
-- +goose StatementBegin
CREATE TABLE eval_experiment_evaluators_new (
    experiment_id TEXT NOT NULL REFERENCES eval_experiments(id) ON DELETE CASCADE,
    evaluator_id TEXT NOT NULL REFERENCES eval_evaluators(id) ON DELETE CASCADE,
    PRIMARY KEY (experiment_id, evaluator_id)
);
INSERT INTO eval_experiment_evaluators_new (experiment_id, evaluator_id)
SELECT experiment_id, evaluator_id FROM eval_experiment_evaluators;
DROP TABLE eval_experiment_evaluators;
ALTER TABLE eval_experiment_evaluators_new RENAME TO eval_experiment_evaluators;

CREATE TABLE eval_run_scores_new (
    id TEXT PRIMARY KEY,
    run_result_id TEXT NOT NULL REFERENCES eval_run_results(id) ON DELETE CASCADE,
    evaluator_id TEXT NOT NULL,
    score REAL NOT NULL,
    choice TEXT,
    passed INTEGER,
    reasoning TEXT,
    created_time INTEGER NOT NULL,
    UNIQUE (run_result_id, evaluator_id)
);
INSERT INTO eval_run_scores_new
    (id, run_result_id, evaluator_id, score, choice, passed, reasoning, created_time)
SELECT id, run_result_id, evaluator_id, score, choice, passed, reasoning, created_time
FROM eval_run_scores;
DROP INDEX IF EXISTS ix_eval_run_scores_run_result_id;
DROP INDEX IF EXISTS ix_eval_run_scores_evaluator_id;
DROP TABLE eval_run_scores;
ALTER TABLE eval_run_scores_new RENAME TO eval_run_scores;
CREATE INDEX ix_eval_run_scores_run_result_id ON eval_run_scores(run_result_id);
CREATE INDEX ix_eval_run_scores_evaluator_id ON eval_run_scores(evaluator_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE eval_experiment_evaluators_old (
    experiment_id TEXT NOT NULL REFERENCES eval_experiments(id) ON DELETE CASCADE,
    evaluator_id TEXT NOT NULL REFERENCES eval_evaluators(id) ON DELETE RESTRICT,
    PRIMARY KEY (experiment_id, evaluator_id)
);
INSERT INTO eval_experiment_evaluators_old (experiment_id, evaluator_id)
SELECT experiment_id, evaluator_id FROM eval_experiment_evaluators;
DROP TABLE eval_experiment_evaluators;
ALTER TABLE eval_experiment_evaluators_old RENAME TO eval_experiment_evaluators;

CREATE TABLE eval_run_scores_old (
    id TEXT PRIMARY KEY,
    run_result_id TEXT NOT NULL REFERENCES eval_run_results(id) ON DELETE CASCADE,
    evaluator_id TEXT NOT NULL REFERENCES eval_evaluators(id) ON DELETE RESTRICT,
    score REAL NOT NULL,
    choice TEXT,
    passed INTEGER,
    reasoning TEXT,
    created_time INTEGER NOT NULL,
    UNIQUE (run_result_id, evaluator_id)
);
INSERT INTO eval_run_scores_old
    (id, run_result_id, evaluator_id, score, choice, passed, reasoning, created_time)
SELECT id, run_result_id, evaluator_id, score, choice, passed, reasoning, created_time
FROM eval_run_scores;
DROP INDEX IF EXISTS ix_eval_run_scores_run_result_id;
DROP INDEX IF EXISTS ix_eval_run_scores_evaluator_id;
DROP TABLE eval_run_scores;
ALTER TABLE eval_run_scores_old RENAME TO eval_run_scores;
CREATE INDEX ix_eval_run_scores_run_result_id ON eval_run_scores(run_result_id);
CREATE INDEX ix_eval_run_scores_evaluator_id ON eval_run_scores(evaluator_id);
-- +goose StatementEnd
