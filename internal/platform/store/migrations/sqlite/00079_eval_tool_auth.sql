
-- +goose Up
CREATE TABLE eval_tool_auth_attribution (
    experiment_id TEXT NOT NULL REFERENCES eval_experiments(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    credential_key TEXT NOT NULL,
    authorized_by TEXT NOT NULL,
    authorized_time BIGINT NOT NULL,
    PRIMARY KEY (experiment_id, agent_name, credential_key)
);

CREATE TABLE eval_tool_auth_agents (
    experiment_id TEXT NOT NULL REFERENCES eval_experiments(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    added_by TEXT NOT NULL,
    added_time BIGINT NOT NULL,
    PRIMARY KEY (experiment_id, agent_name)
);

-- +goose Down
DROP TABLE eval_tool_auth_agents;
DROP TABLE eval_tool_auth_attribution;
