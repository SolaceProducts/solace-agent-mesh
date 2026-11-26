-- +goose Up
UPDATE connectors SET subtype = 'bedrock' WHERE type = 'knowledge_base' AND subtype = 'bedrock_kb';

-- +goose Down
UPDATE connectors SET subtype = 'bedrock_kb' WHERE type = 'knowledge_base' AND subtype = 'bedrock';
