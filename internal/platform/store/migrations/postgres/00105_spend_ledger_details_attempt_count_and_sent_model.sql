-- +goose Up

ALTER TABLE token_spend_ledger_details ADD COLUMN IF NOT EXISTS attempt_count INTEGER;

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = current_schema() AND table_name = 'token_spend_ledger_details'
               AND column_name = 'requested_model_name') THEN
    ALTER TABLE token_spend_ledger_details RENAME COLUMN requested_model_name TO sent_model_name;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = current_schema() AND table_name = 'token_spend_ledger_details'
               AND column_name = 'sent_model_name') THEN
    ALTER TABLE token_spend_ledger_details RENAME COLUMN sent_model_name TO requested_model_name;
  END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE token_spend_ledger_details DROP COLUMN IF EXISTS attempt_count;
