-- +goose Up

UPDATE agents SET type = 'pro_code' WHERE type = 'pro-code';

ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy', 'pro_code'));

-- +goose Down
ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy', 'pro-code'));

UPDATE agents SET type = 'pro-code' WHERE type = 'pro_code';
