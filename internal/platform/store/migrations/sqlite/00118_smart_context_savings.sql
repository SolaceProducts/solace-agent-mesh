
-- +goose Up
ALTER TABLE token_spend_ledger ADD COLUMN avoided_input_tokens INTEGER;
ALTER TABLE token_spend_ledger ADD COLUMN avoided_input_cost INTEGER;

ALTER TABLE token_spend_daily ADD COLUMN avoided_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily ADD COLUMN avoided_input_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily ADD COLUMN measured_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily ADD COLUMN priced_measured_calls INTEGER NOT NULL DEFAULT 0;

ALTER TABLE token_spend_daily_models ADD COLUMN avoided_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily_models ADD COLUMN avoided_input_cost INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily_models ADD COLUMN measured_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily_models ADD COLUMN priced_measured_calls INTEGER NOT NULL DEFAULT 0;

UPDATE token_spend_rollup_state SET grain_version = 3, refreshed_time = NULL, reconciled_time = NULL WHERE id = 1;

-- +goose Down
UPDATE token_spend_rollup_state SET grain_version = 2, refreshed_time = NULL, reconciled_time = NULL WHERE id = 1;

ALTER TABLE token_spend_daily_models DROP COLUMN priced_measured_calls;
ALTER TABLE token_spend_daily_models DROP COLUMN measured_calls;
ALTER TABLE token_spend_daily_models DROP COLUMN avoided_input_cost;
ALTER TABLE token_spend_daily_models DROP COLUMN avoided_input_tokens;

ALTER TABLE token_spend_daily DROP COLUMN priced_measured_calls;
ALTER TABLE token_spend_daily DROP COLUMN measured_calls;
ALTER TABLE token_spend_daily DROP COLUMN avoided_input_cost;
ALTER TABLE token_spend_daily DROP COLUMN avoided_input_tokens;

ALTER TABLE token_spend_ledger DROP COLUMN avoided_input_cost;
ALTER TABLE token_spend_ledger DROP COLUMN avoided_input_tokens;
