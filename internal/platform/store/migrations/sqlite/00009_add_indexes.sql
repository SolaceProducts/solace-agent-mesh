-- +goose Up

CREATE INDEX idx_agents_created_time ON agents(created_time DESC);
CREATE INDEX idx_connectors_created_time ON connectors(created_time DESC);
CREATE INDEX idx_gateways_created_time ON gateways(created_time DESC);
CREATE INDEX idx_workflows_created_time ON workflows(created_time DESC);
CREATE INDEX idx_a2a_agents_created_time ON a2a_agents(created_time DESC);
CREATE INDEX idx_skills_created_time ON skills(created_time DESC);
CREATE INDEX idx_tools_created_time ON tools(created_time DESC);

CREATE INDEX idx_agent_deployments_agent_time ON agent_deployments(agent_id, created_time DESC);
CREATE INDEX idx_agent_deployments_status_time ON agent_deployments(status, created_time);
CREATE INDEX idx_gateway_deployments_gw_time ON gateway_deployments(gateway_id, created_time DESC);
CREATE INDEX idx_gateway_deployments_status_time ON gateway_deployments(status, created_time);
CREATE INDEX idx_workflow_deployments_wf_time ON workflow_deployments(workflow_id, created_time DESC);
CREATE INDEX idx_workflow_deployments_status_time ON workflow_deployments(status, created_time);

CREATE INDEX idx_agent_connectors_connector ON agent_connectors(connector_id);
CREATE INDEX idx_a2a_agent_deployments_deploy ON a2a_agent_deployments(agent_deployment_id);

CREATE INDEX idx_connectors_type ON connectors(type, created_time DESC);
CREATE INDEX idx_gateways_type ON gateways(type, created_time DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_gateways_type;
DROP INDEX IF EXISTS idx_connectors_type;
DROP INDEX IF EXISTS idx_a2a_agent_deployments_deploy;
DROP INDEX IF EXISTS idx_agent_connectors_connector;
DROP INDEX IF EXISTS idx_workflow_deployments_status_time;
DROP INDEX IF EXISTS idx_workflow_deployments_wf_time;
DROP INDEX IF EXISTS idx_gateway_deployments_status_time;
DROP INDEX IF EXISTS idx_gateway_deployments_gw_time;
DROP INDEX IF EXISTS idx_agent_deployments_status_time;
DROP INDEX IF EXISTS idx_agent_deployments_agent_time;
DROP INDEX IF EXISTS idx_tools_created_time;
DROP INDEX IF EXISTS idx_skills_created_time;
DROP INDEX IF EXISTS idx_a2a_agents_created_time;
DROP INDEX IF EXISTS idx_workflows_created_time;
DROP INDEX IF EXISTS idx_gateways_created_time;
DROP INDEX IF EXISTS idx_connectors_created_time;
DROP INDEX IF EXISTS idx_agents_created_time;
