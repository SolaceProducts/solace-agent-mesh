-- +goose Up

ALTER TABLE token_spend_ledger_details ADD COLUMN cache_writes_reported BOOLEAN;

-- +goose Down

ALTER TABLE token_spend_ledger_details DROP COLUMN cache_writes_reported;
