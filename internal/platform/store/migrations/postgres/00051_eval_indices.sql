
-- +goose Up
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_eval_experiments_target_agent
    ON eval_experiments(target_agent);
CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_eval_runs_run_group_started
    ON eval_runs(run_group_id, started_time);
DROP INDEX CONCURRENTLY IF EXISTS ix_eval_runs_run_group_id;

-- +goose Down
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_eval_runs_run_group_id
    ON eval_runs(run_group_id);
DROP INDEX CONCURRENTLY IF EXISTS ix_eval_runs_run_group_started;
DROP INDEX CONCURRENTLY IF EXISTS ix_eval_experiments_target_agent;
