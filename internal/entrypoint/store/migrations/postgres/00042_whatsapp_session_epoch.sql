-- +goose Up

ALTER TABLE whatsapp_conversations ADD COLUMN session_epoch BIGINT NOT NULL DEFAULT 0;

UPDATE whatsapp_conversations SET session_epoch = last_inbound_at WHERE session_epoch = 0;

-- +goose Down

ALTER TABLE whatsapp_conversations DROP COLUMN IF EXISTS session_epoch;
