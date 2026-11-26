-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
  col_type text;
BEGIN
  -- Check if conversion is needed (skip if already UUID).
  SELECT data_type INTO col_type FROM information_schema.columns
    WHERE table_name='agents' AND column_name='id';
  IF col_type = 'uuid' THEN
    RETURN;
  END IF;

  -- Step 1: Drop FK constraints (IF EXISTS for Python DB compatibility).
  ALTER TABLE agent_toolsets DROP CONSTRAINT IF EXISTS agent_toolsets_agent_id_fkey;
  ALTER TABLE agent_input_modes DROP CONSTRAINT IF EXISTS agent_input_modes_agent_id_fkey;
  ALTER TABLE agent_output_modes DROP CONSTRAINT IF EXISTS agent_output_modes_agent_id_fkey;
  ALTER TABLE agent_connectors DROP CONSTRAINT IF EXISTS agent_connectors_agent_id_fkey;
  ALTER TABLE agent_connectors DROP CONSTRAINT IF EXISTS agent_connectors_connector_id_fkey;
  ALTER TABLE agent_deployments DROP CONSTRAINT IF EXISTS agent_deployments_agent_id_fkey;
  ALTER TABLE gateway_deployments DROP CONSTRAINT IF EXISTS gateway_deployments_gateway_id_fkey;
  ALTER TABLE a2a_agents DROP CONSTRAINT IF EXISTS a2a_agents_agent_id_fkey;
  ALTER TABLE a2a_agent_deployments DROP CONSTRAINT IF EXISTS a2a_agent_deployments_a2a_agent_id_fkey;
  ALTER TABLE a2a_agent_deployments DROP CONSTRAINT IF EXISTS a2a_agent_deployments_agent_deployment_id_fkey;
  ALTER TABLE workflow_deployments DROP CONSTRAINT IF EXISTS workflow_deployments_workflow_id_fkey;

  -- Step 2: ALTER COLUMN TYPE UUID.
  ALTER TABLE agents ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE agent_toolsets ALTER COLUMN agent_id TYPE UUID USING agent_id::uuid;
  ALTER TABLE agent_input_modes ALTER COLUMN agent_id TYPE UUID USING agent_id::uuid;
  ALTER TABLE agent_output_modes ALTER COLUMN agent_id TYPE UUID USING agent_id::uuid;
  ALTER TABLE connectors ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE agent_connectors ALTER COLUMN agent_id TYPE UUID USING agent_id::uuid;
  ALTER TABLE agent_connectors ALTER COLUMN connector_id TYPE UUID USING connector_id::uuid;
  ALTER TABLE agent_deployments ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE agent_deployments ALTER COLUMN agent_id TYPE UUID USING agent_id::uuid;
  ALTER TABLE gateways ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE gateway_deployments ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE gateway_deployments ALTER COLUMN gateway_id TYPE UUID USING gateway_id::uuid;
  ALTER TABLE a2a_agents ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE a2a_agents ALTER COLUMN agent_id TYPE UUID USING agent_id::uuid;
  ALTER TABLE a2a_agent_deployments ALTER COLUMN a2a_agent_id TYPE UUID USING a2a_agent_id::uuid;
  ALTER TABLE a2a_agent_deployments ALTER COLUMN agent_deployment_id TYPE UUID USING agent_deployment_id::uuid;
  ALTER TABLE workflows ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE workflow_deployments ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE workflow_deployments ALTER COLUMN workflow_id TYPE UUID USING workflow_id::uuid;
  ALTER TABLE skills ALTER COLUMN id TYPE UUID USING id::uuid;
  ALTER TABLE tools ALTER COLUMN id TYPE UUID USING id::uuid;

  -- Outbox: only convert if Go schema (aggregate_id column exists).
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='outbox_events' AND column_name='aggregate_id') THEN
    ALTER TABLE outbox_events ALTER COLUMN id TYPE UUID USING id::uuid;
    ALTER TABLE outbox_events ALTER COLUMN aggregate_id TYPE UUID USING aggregate_id::uuid;
  END IF;

  -- Step 3: Re-add FK constraints.
  ALTER TABLE agent_toolsets ADD CONSTRAINT agent_toolsets_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_input_modes ADD CONSTRAINT agent_input_modes_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_output_modes ADD CONSTRAINT agent_output_modes_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_connectors ADD CONSTRAINT agent_connectors_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_connectors ADD CONSTRAINT agent_connectors_connector_id_fkey
      FOREIGN KEY (connector_id) REFERENCES connectors(id) ON DELETE CASCADE;
  ALTER TABLE agent_deployments ADD CONSTRAINT agent_deployments_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE gateway_deployments ADD CONSTRAINT gateway_deployments_gateway_id_fkey
      FOREIGN KEY (gateway_id) REFERENCES gateways(id) ON DELETE CASCADE;
  ALTER TABLE a2a_agents ADD CONSTRAINT a2a_agents_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE SET NULL;
  ALTER TABLE a2a_agent_deployments ADD CONSTRAINT a2a_agent_deployments_a2a_agent_id_fkey
      FOREIGN KEY (a2a_agent_id) REFERENCES a2a_agents(id) ON DELETE CASCADE;
  ALTER TABLE a2a_agent_deployments ADD CONSTRAINT a2a_agent_deployments_agent_deployment_id_fkey
      FOREIGN KEY (agent_deployment_id) REFERENCES agent_deployments(id) ON DELETE CASCADE;
  ALTER TABLE workflow_deployments ADD CONSTRAINT workflow_deployments_workflow_id_fkey
      FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
  col_type text;
BEGIN
  SELECT data_type INTO col_type FROM information_schema.columns
    WHERE table_name='agents' AND column_name='id';
  IF col_type != 'uuid' THEN
    RETURN;
  END IF;

  ALTER TABLE agent_toolsets DROP CONSTRAINT IF EXISTS agent_toolsets_agent_id_fkey;
  ALTER TABLE agent_input_modes DROP CONSTRAINT IF EXISTS agent_input_modes_agent_id_fkey;
  ALTER TABLE agent_output_modes DROP CONSTRAINT IF EXISTS agent_output_modes_agent_id_fkey;
  ALTER TABLE agent_connectors DROP CONSTRAINT IF EXISTS agent_connectors_agent_id_fkey;
  ALTER TABLE agent_connectors DROP CONSTRAINT IF EXISTS agent_connectors_connector_id_fkey;
  ALTER TABLE agent_deployments DROP CONSTRAINT IF EXISTS agent_deployments_agent_id_fkey;
  ALTER TABLE gateway_deployments DROP CONSTRAINT IF EXISTS gateway_deployments_gateway_id_fkey;
  ALTER TABLE a2a_agents DROP CONSTRAINT IF EXISTS a2a_agents_agent_id_fkey;
  ALTER TABLE a2a_agent_deployments DROP CONSTRAINT IF EXISTS a2a_agent_deployments_a2a_agent_id_fkey;
  ALTER TABLE a2a_agent_deployments DROP CONSTRAINT IF EXISTS a2a_agent_deployments_agent_deployment_id_fkey;
  ALTER TABLE workflow_deployments DROP CONSTRAINT IF EXISTS workflow_deployments_workflow_id_fkey;

  ALTER TABLE agents ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE agent_toolsets ALTER COLUMN agent_id TYPE TEXT USING agent_id::text;
  ALTER TABLE agent_input_modes ALTER COLUMN agent_id TYPE TEXT USING agent_id::text;
  ALTER TABLE agent_output_modes ALTER COLUMN agent_id TYPE TEXT USING agent_id::text;
  ALTER TABLE connectors ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE agent_connectors ALTER COLUMN agent_id TYPE TEXT USING agent_id::text;
  ALTER TABLE agent_connectors ALTER COLUMN connector_id TYPE TEXT USING connector_id::text;
  ALTER TABLE agent_deployments ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE agent_deployments ALTER COLUMN agent_id TYPE TEXT USING agent_id::text;
  ALTER TABLE gateways ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE gateway_deployments ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE gateway_deployments ALTER COLUMN gateway_id TYPE TEXT USING gateway_id::text;
  ALTER TABLE a2a_agents ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE a2a_agents ALTER COLUMN agent_id TYPE TEXT USING agent_id::text;
  ALTER TABLE a2a_agent_deployments ALTER COLUMN a2a_agent_id TYPE TEXT USING a2a_agent_id::text;
  ALTER TABLE a2a_agent_deployments ALTER COLUMN agent_deployment_id TYPE TEXT USING agent_deployment_id::text;
  ALTER TABLE workflows ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE workflow_deployments ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE workflow_deployments ALTER COLUMN workflow_id TYPE TEXT USING workflow_id::text;
  ALTER TABLE skills ALTER COLUMN id TYPE TEXT USING id::text;
  ALTER TABLE tools ALTER COLUMN id TYPE TEXT USING id::text;

  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='outbox_events' AND column_name='aggregate_id') THEN
    ALTER TABLE outbox_events ALTER COLUMN id TYPE TEXT USING id::text;
    ALTER TABLE outbox_events ALTER COLUMN aggregate_id TYPE TEXT USING aggregate_id::text;
  END IF;

  ALTER TABLE agent_toolsets ADD CONSTRAINT agent_toolsets_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_input_modes ADD CONSTRAINT agent_input_modes_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_output_modes ADD CONSTRAINT agent_output_modes_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_connectors ADD CONSTRAINT agent_connectors_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE agent_connectors ADD CONSTRAINT agent_connectors_connector_id_fkey
      FOREIGN KEY (connector_id) REFERENCES connectors(id) ON DELETE CASCADE;
  ALTER TABLE agent_deployments ADD CONSTRAINT agent_deployments_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;
  ALTER TABLE gateway_deployments ADD CONSTRAINT gateway_deployments_gateway_id_fkey
      FOREIGN KEY (gateway_id) REFERENCES gateways(id) ON DELETE CASCADE;
  ALTER TABLE a2a_agents ADD CONSTRAINT a2a_agents_agent_id_fkey
      FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE SET NULL;
  ALTER TABLE a2a_agent_deployments ADD CONSTRAINT a2a_agent_deployments_a2a_agent_id_fkey
      FOREIGN KEY (a2a_agent_id) REFERENCES a2a_agents(id) ON DELETE CASCADE;
  ALTER TABLE a2a_agent_deployments ADD CONSTRAINT a2a_agent_deployments_agent_deployment_id_fkey
      FOREIGN KEY (agent_deployment_id) REFERENCES agent_deployments(id) ON DELETE CASCADE;
  ALTER TABLE workflow_deployments ADD CONSTRAINT workflow_deployments_workflow_id_fkey
      FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE;
END $$;
-- +goose StatementEnd
