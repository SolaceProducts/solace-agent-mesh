
-- +goose Up
ALTER TABLE eval_experiment_evaluators
    DROP CONSTRAINT IF EXISTS eval_experiment_evaluators_evaluator_id_fkey;
ALTER TABLE eval_experiment_evaluators
    ADD CONSTRAINT eval_experiment_evaluators_evaluator_id_fkey
    FOREIGN KEY (evaluator_id) REFERENCES eval_evaluators(id) ON DELETE CASCADE;

ALTER TABLE eval_run_scores
    DROP CONSTRAINT IF EXISTS eval_run_scores_evaluator_id_fkey;

-- +goose Down
ALTER TABLE eval_experiment_evaluators
    DROP CONSTRAINT IF EXISTS eval_experiment_evaluators_evaluator_id_fkey;
ALTER TABLE eval_experiment_evaluators
    ADD CONSTRAINT eval_experiment_evaluators_evaluator_id_fkey
    FOREIGN KEY (evaluator_id) REFERENCES eval_evaluators(id) ON DELETE RESTRICT;

ALTER TABLE eval_run_scores
    ADD CONSTRAINT eval_run_scores_evaluator_id_fkey
    FOREIGN KEY (evaluator_id) REFERENCES eval_evaluators(id) ON DELETE RESTRICT;
