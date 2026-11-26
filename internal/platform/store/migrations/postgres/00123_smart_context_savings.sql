
-- +goose Up
ALTER TABLE token_spend_ledger ADD COLUMN IF NOT EXISTS avoided_input_tokens BIGINT;
ALTER TABLE token_spend_ledger ADD COLUMN IF NOT EXISTS avoided_input_cost BIGINT;

ALTER TABLE token_spend_daily ADD COLUMN IF NOT EXISTS avoided_input_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily ADD COLUMN IF NOT EXISTS avoided_input_cost BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily ADD COLUMN IF NOT EXISTS measured_calls BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily ADD COLUMN IF NOT EXISTS priced_measured_calls BIGINT NOT NULL DEFAULT 0;

ALTER TABLE token_spend_daily_models ADD COLUMN IF NOT EXISTS avoided_input_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily_models ADD COLUMN IF NOT EXISTS avoided_input_cost BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily_models ADD COLUMN IF NOT EXISTS measured_calls BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_spend_daily_models ADD COLUMN IF NOT EXISTS priced_measured_calls BIGINT NOT NULL DEFAULT 0;

UPDATE token_spend_rollup_state SET grain_version = 3, refreshed_time = NULL, reconciled_time = NULL WHERE id = 1;

-- +goose Down
UPDATE token_spend_rollup_state SET grain_version = 2, refreshed_time = NULL, reconciled_time = NULL WHERE id = 1;

ALTER TABLE token_spend_daily_models DROP COLUMN IF EXISTS priced_measured_calls;
ALTER TABLE token_spend_daily_models DROP COLUMN IF EXISTS measured_calls;
ALTER TABLE token_spend_daily_models DROP COLUMN IF EXISTS avoided_input_cost;
ALTER TABLE token_spend_daily_models DROP COLUMN IF EXISTS avoided_input_tokens;

ALTER TABLE token_spend_daily DROP COLUMN IF EXISTS priced_measured_calls;
ALTER TABLE token_spend_daily DROP COLUMN IF EXISTS measured_calls;
ALTER TABLE token_spend_daily DROP COLUMN IF EXISTS avoided_input_cost;
ALTER TABLE token_spend_daily DROP COLUMN IF EXISTS avoided_input_tokens;

ALTER TABLE token_spend_ledger DROP COLUMN IF EXISTS avoided_input_cost;
ALTER TABLE token_spend_ledger DROP COLUMN IF EXISTS avoided_input_tokens;
