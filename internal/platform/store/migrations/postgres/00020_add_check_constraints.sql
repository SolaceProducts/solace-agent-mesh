-- +goose Up

ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy', 'pro-code'));

ALTER TABLE connectors ADD CONSTRAINT check_connector_name_length
  CHECK (LENGTH(name) >= 3 AND LENGTH(name) <= 255);
ALTER TABLE connectors ADD CONSTRAINT check_connector_description_length
  CHECK (description IS NULL OR (LENGTH(description) >= 10 AND LENGTH(description) <= 1000));
ALTER TABLE connectors ADD CONSTRAINT check_connector_type_length
  CHECK (LENGTH(type) <= 50);
ALTER TABLE connectors ADD CONSTRAINT check_connector_subtype_length
  CHECK (subtype IS NULL OR LENGTH(subtype) <= 50);

ALTER TABLE gateways ADD CONSTRAINT check_gateway_name_length
  CHECK (LENGTH(name) >= 3 AND LENGTH(name) <= 255);
ALTER TABLE gateways ADD CONSTRAINT check_gateway_description_length
  CHECK (description IS NULL OR (LENGTH(description) >= 10 AND LENGTH(description) <= 1000));

ALTER TABLE a2a_agents ADD CONSTRAINT check_a2a_agent_card_location
  CHECK (agent_card_location IN ('well_known', 'custom', 'none'));
ALTER TABLE a2a_agents ADD CONSTRAINT check_a2a_agent_name_length
  CHECK (LENGTH(name) >= 3 AND LENGTH(name) <= 40);
ALTER TABLE a2a_agents ADD CONSTRAINT check_a2a_request_timeout_range
  CHECK (request_timeout_seconds >= 10 AND request_timeout_seconds <= 600);

-- +goose Down

ALTER TABLE a2a_agents DROP CONSTRAINT IF EXISTS check_a2a_request_timeout_range;
ALTER TABLE a2a_agents DROP CONSTRAINT IF EXISTS check_a2a_agent_name_length;
ALTER TABLE a2a_agents DROP CONSTRAINT IF EXISTS check_a2a_agent_card_location;
ALTER TABLE gateways DROP CONSTRAINT IF EXISTS check_gateway_description_length;
ALTER TABLE gateways DROP CONSTRAINT IF EXISTS check_gateway_name_length;
ALTER TABLE connectors DROP CONSTRAINT IF EXISTS check_connector_subtype_length;
ALTER TABLE connectors DROP CONSTRAINT IF EXISTS check_connector_type_length;
ALTER TABLE connectors DROP CONSTRAINT IF EXISTS check_connector_description_length;
ALTER TABLE connectors DROP CONSTRAINT IF EXISTS check_connector_name_length;
ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
