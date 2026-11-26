-- +goose Up

CREATE TABLE eval_datasets (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE eval_examples (
    id UUID PRIMARY KEY,
    dataset_id UUID NOT NULL REFERENCES eval_datasets(id) ON DELETE CASCADE,
    sequence_number INTEGER NOT NULL,
    prompt TEXT NOT NULL,
    expected_response TEXT,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,
    CONSTRAINT uq_eval_examples_dataset_sequence UNIQUE (dataset_id, sequence_number)
);

CREATE INDEX ix_eval_examples_dataset_id ON eval_examples(dataset_id);

CREATE TABLE eval_evaluators (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    type TEXT NOT NULL,
    heuristic_type TEXT,
    is_default INTEGER NOT NULL DEFAULT 0,
    model TEXT,
    prompt_template TEXT,
    choice_scores TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,
    CONSTRAINT ck_eval_evaluators_llm_judge_required_fields
        CHECK (type != 'llm_judge' OR (prompt_template IS NOT NULL AND choice_scores IS NOT NULL))
);

CREATE TABLE eval_experiments (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    dataset_id UUID REFERENCES eval_datasets(id) ON DELETE SET NULL,
    target_agent TEXT NOT NULL,
    models TEXT NOT NULL,
    runs_per_example INTEGER NOT NULL DEFAULT 1,
    max_workers INTEGER NOT NULL DEFAULT 4,
    primary_evaluator_id UUID REFERENCES eval_evaluators(id) ON DELETE SET NULL,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE INDEX ix_eval_experiments_dataset_id ON eval_experiments(dataset_id);

CREATE TABLE eval_experiment_evaluators (
    experiment_id UUID NOT NULL REFERENCES eval_experiments(id) ON DELETE CASCADE,
    evaluator_id UUID NOT NULL REFERENCES eval_evaluators(id) ON DELETE RESTRICT,
    PRIMARY KEY (experiment_id, evaluator_id)
);

CREATE TABLE eval_runs (
    id UUID PRIMARY KEY,
    experiment_id UUID NOT NULL REFERENCES eval_experiments(id) ON DELETE CASCADE,
    run_group_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    progress_current INTEGER NOT NULL DEFAULT 0,
    progress_total INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    summary_scores TEXT,
    model TEXT NOT NULL,
    config_snapshot TEXT,
    evaluator_snapshot TEXT,
    run_name TEXT,
    source TEXT NOT NULL DEFAULT 'manual',
    started_by TEXT NOT NULL,
    started_time BIGINT NOT NULL,
    completed_time BIGINT,
    coordinator_started_at BIGINT
);

CREATE INDEX ix_eval_runs_experiment_id ON eval_runs(experiment_id);
CREATE INDEX ix_eval_runs_status ON eval_runs(status);
CREATE INDEX ix_eval_runs_run_group_id ON eval_runs(run_group_id);

CREATE TABLE eval_run_results (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    example_id UUID NOT NULL REFERENCES eval_examples(id) ON DELETE CASCADE,
    trial_index INTEGER NOT NULL DEFAULT 0,
    input_prompt TEXT,
    expected_response TEXT,
    target_agent TEXT,
    agent_response TEXT,
    resolved_model_name TEXT,
    execution_data_key TEXT,
    task_events_key TEXT,
    duration_seconds DOUBLE PRECISION,
    error_message TEXT,
    created_time BIGINT NOT NULL,
    CONSTRAINT uq_run_example_trial UNIQUE (run_id, example_id, trial_index)
);

CREATE INDEX ix_eval_run_results_run_id ON eval_run_results(run_id);
CREATE INDEX ix_eval_run_results_example_id ON eval_run_results(example_id);

CREATE TABLE eval_run_scores (
    id UUID PRIMARY KEY,
    run_result_id UUID NOT NULL REFERENCES eval_run_results(id) ON DELETE CASCADE,
    evaluator_id UUID NOT NULL REFERENCES eval_evaluators(id) ON DELETE RESTRICT,
    score DOUBLE PRECISION NOT NULL,
    choice TEXT,
    passed INTEGER,
    reasoning TEXT,
    created_time BIGINT NOT NULL,
    CONSTRAINT uq_run_score_result_evaluator UNIQUE (run_result_id, evaluator_id)
);

CREATE INDEX ix_eval_run_scores_run_result_id ON eval_run_scores(run_result_id);
CREATE INDEX ix_eval_run_scores_evaluator_id ON eval_run_scores(evaluator_id);

-- +goose Down

DROP TABLE IF EXISTS eval_run_scores;
DROP TABLE IF EXISTS eval_run_results;
DROP TABLE IF EXISTS eval_runs;
DROP TABLE IF EXISTS eval_experiment_evaluators;
DROP TABLE IF EXISTS eval_experiments;
DROP TABLE IF EXISTS eval_evaluators;
DROP TABLE IF EXISTS eval_examples;
DROP TABLE IF EXISTS eval_datasets;
