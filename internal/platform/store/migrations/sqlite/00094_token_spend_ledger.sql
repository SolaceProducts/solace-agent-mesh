
-- +goose Up
CREATE TABLE IF NOT EXISTS token_spend_ledger (
    id                    TEXT PRIMARY KEY,
    completed_time        INTEGER NOT NULL,
    user_id               TEXT,
    user_sub              TEXT,
    agent_id              TEXT,
    agent_name            TEXT,
    workflow_id           TEXT,
    workflow_name         TEXT,
    root_kind             TEXT,
    root_id               TEXT,
    root_name             TEXT,
    root_node_id          TEXT,
    entrypoint_id         TEXT,
    entrypoint_name       TEXT,
    channel               TEXT,
    trigger_kind          TEXT,
    trigger_id            TEXT,
    surface               TEXT NOT NULL,
    model_config_id       TEXT,
    model_name            TEXT,
    task_id               TEXT,
    trace_id              TEXT,
    uncached_input_tokens INTEGER NOT NULL,
    output_tokens         INTEGER NOT NULL,
    cache_read_tokens     INTEGER NOT NULL,
    cache_write_tokens    INTEGER NOT NULL,
    uncached_input_cost   INTEGER,
    output_cost           INTEGER,
    cache_read_cost       INTEGER,
    cache_write_cost      INTEGER,
    total_cost            INTEGER GENERATED ALWAYS AS
                          (uncached_input_cost + output_cost + cache_read_cost + cache_write_cost) STORED,
    CONSTRAINT token_spend_ledger_cost_together CHECK (
        (uncached_input_cost IS NULL) = (output_cost IS NULL)
        AND (uncached_input_cost IS NULL) = (cache_read_cost IS NULL)
        AND (uncached_input_cost IS NULL) = (cache_write_cost IS NULL)),
    CONSTRAINT token_spend_ledger_one_user CHECK ((user_id IS NULL) <> (user_sub IS NULL)),
    CONSTRAINT token_spend_ledger_one_model CHECK ((model_config_id IS NULL) <> (model_name IS NULL)),
    CONSTRAINT token_spend_ledger_agent_pair CHECK (agent_id IS NULL OR agent_name IS NULL),
    CONSTRAINT token_spend_ledger_workflow_pair CHECK (workflow_id IS NULL OR workflow_name IS NULL),
    CONSTRAINT token_spend_ledger_entrypoint_pair CHECK (entrypoint_id IS NULL OR entrypoint_name IS NULL),
    CONSTRAINT token_spend_ledger_root CHECK (
        (root_id IS NULL OR root_name IS NULL)
        AND (root_kind IS NULL) = (root_id IS NULL AND root_name IS NULL)),
    CONSTRAINT token_spend_ledger_root_node CHECK (
        root_node_id IS NULL OR (root_id IS NULL) <> (root_name IS NULL)),
    CONSTRAINT token_spend_ledger_tokens_non_negative CHECK (
        uncached_input_tokens >= 0 AND output_tokens >= 0
        AND cache_read_tokens >= 0 AND cache_write_tokens >= 0),
    CONSTRAINT token_spend_ledger_costs_non_negative CHECK (
        (uncached_input_cost IS NULL OR uncached_input_cost >= 0)
        AND (output_cost IS NULL OR output_cost >= 0)
        AND (cache_read_cost IS NULL OR cache_read_cost >= 0)
        AND (cache_write_cost IS NULL OR cache_write_cost >= 0))
);

CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_completed
    ON token_spend_ledger (completed_time);
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_user
    ON token_spend_ledger (user_id, completed_time) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_user_sub
    ON token_spend_ledger (user_sub, completed_time) WHERE user_sub IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_agent
    ON token_spend_ledger (agent_id, completed_time) WHERE agent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_agent_name
    ON token_spend_ledger (agent_name, completed_time) WHERE agent_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_workflow
    ON token_spend_ledger (workflow_id, completed_time) WHERE workflow_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_workflow_name
    ON token_spend_ledger (workflow_name, completed_time) WHERE workflow_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_root
    ON token_spend_ledger (root_id, completed_time) WHERE root_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_root_name
    ON token_spend_ledger (root_name, completed_time) WHERE root_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_entrypoint
    ON token_spend_ledger (entrypoint_id, completed_time) WHERE entrypoint_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_entrypoint_name
    ON token_spend_ledger (entrypoint_name, completed_time) WHERE entrypoint_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_model
    ON token_spend_ledger (model_config_id, completed_time) WHERE model_config_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_ledger_trace
    ON token_spend_ledger (trace_id) WHERE trace_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS token_spend_ledger_details (
    id                       TEXT PRIMARY KEY REFERENCES token_spend_ledger (id) ON DELETE CASCADE,
    model_key                TEXT,
    requested_model_name     TEXT,
    tool_name                TEXT,
    workflow_node_id         TEXT,
    parent_task_id           TEXT,
    session_id               TEXT,
    resolved_model_name      TEXT,
    resolved_model_provider  TEXT,
    resolved_agent_name      TEXT,
    resolved_workflow_name   TEXT,
    resolved_root_name       TEXT,
    resolved_entrypoint_name TEXT
);

-- +goose Down
DROP TABLE IF EXISTS token_spend_ledger_details;
DROP TABLE IF EXISTS token_spend_ledger;
