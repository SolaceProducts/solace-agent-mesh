-- +goose Up
DELETE FROM agents WHERE type = 'pro_code';
ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy'));

-- +goose Down
ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy', 'pro_code'));
