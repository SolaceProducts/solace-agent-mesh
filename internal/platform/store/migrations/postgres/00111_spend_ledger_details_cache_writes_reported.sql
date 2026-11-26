-- +goose Up

ALTER TABLE token_spend_ledger_details ADD COLUMN IF NOT EXISTS cache_writes_reported BOOLEAN;

-- +goose Down

ALTER TABLE token_spend_ledger_details DROP COLUMN IF EXISTS cache_writes_reported;
