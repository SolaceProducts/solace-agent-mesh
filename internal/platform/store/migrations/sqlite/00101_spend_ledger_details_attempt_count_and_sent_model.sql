-- +goose Up

ALTER TABLE token_spend_ledger_details ADD COLUMN attempt_count INTEGER;

ALTER TABLE token_spend_ledger_details RENAME COLUMN requested_model_name TO sent_model_name;

-- +goose Down

ALTER TABLE token_spend_ledger_details RENAME COLUMN sent_model_name TO requested_model_name;

ALTER TABLE token_spend_ledger_details DROP COLUMN attempt_count;
