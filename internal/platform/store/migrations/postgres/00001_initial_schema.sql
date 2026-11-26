-- +goose Up

CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    type TEXT NOT NULL DEFAULT 'standard',
    system_prompt TEXT,
    skills TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_toolsets (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    toolset TEXT NOT NULL,
    PRIMARY KEY (agent_id, toolset)
);

CREATE TABLE IF NOT EXISTS agent_input_modes (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    PRIMARY KEY (agent_id, mode)
);

CREATE TABLE IF NOT EXISTS agent_output_modes (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    PRIMARY KEY (agent_id, mode)
);

CREATE TABLE IF NOT EXISTS connectors (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    type TEXT NOT NULL,
    subtype TEXT,
    config_values TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_connectors (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, connector_id)
);

CREATE TABLE IF NOT EXISTS agent_deployments (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    action TEXT NOT NULL,
    agent_config_snapshot TEXT,
    error_message TEXT,
    deployer_version TEXT,
    created_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS gateways (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    type TEXT NOT NULL,
    gateway_runtime_id TEXT,
    config_values TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS gateway_deployments (
    id TEXT PRIMARY KEY,
    gateway_id TEXT NOT NULL REFERENCES gateways(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    action TEXT NOT NULL,
    gateway_config_snapshot TEXT,
    error_message TEXT,
    deployer_version TEXT,
    created_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS a2a_agents (
    id TEXT PRIMARY KEY,
    agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
    agent_url TEXT NOT NULL,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    agent_card_location TEXT NOT NULL DEFAULT 'well_known',
    agent_card_url TEXT,
    agent_card_auth_type TEXT,
    agent_card_auth_config TEXT,
    agent_card_headers TEXT,
    agent_auth_type TEXT,
    agent_auth_config TEXT,
    task_headers TEXT,
    request_timeout_seconds INTEGER DEFAULT 120,
    skills TEXT,
    input_modes TEXT,
    output_modes TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS a2a_agent_deployments (
    a2a_agent_id TEXT NOT NULL REFERENCES a2a_agents(id) ON DELETE CASCADE,
    agent_deployment_id TEXT NOT NULL REFERENCES agent_deployments(id) ON DELETE CASCADE,
    action TEXT,
    config_snapshot TEXT,
    PRIMARY KEY (a2a_agent_id, agent_deployment_id)
);

-- +goose Down

DROP TABLE IF EXISTS a2a_agent_deployments;
DROP TABLE IF EXISTS a2a_agents;
DROP TABLE IF EXISTS gateway_deployments;
DROP TABLE IF EXISTS gateways;
DROP TABLE IF EXISTS agent_deployments;
DROP TABLE IF EXISTS agent_connectors;
DROP TABLE IF EXISTS connectors;
DROP TABLE IF EXISTS agent_output_modes;
DROP TABLE IF EXISTS agent_input_modes;
DROP TABLE IF EXISTS agent_toolsets;
DROP TABLE IF EXISTS agents;
