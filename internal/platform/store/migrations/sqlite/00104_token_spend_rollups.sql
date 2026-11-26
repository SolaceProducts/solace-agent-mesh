
-- +goose Up
CREATE TABLE IF NOT EXISTS token_spend_daily (
    day                   INTEGER NOT NULL,
    grain_key             TEXT NOT NULL,
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
    model_config_id       TEXT,
    model_name            TEXT,
    user_label            TEXT,
    agent_label           TEXT,
    root_label            TEXT,
    model_label           TEXT,
    calls                 INTEGER NOT NULL,
    unpriced_calls        INTEGER NOT NULL,
    uncached_input_tokens INTEGER NOT NULL,
    output_tokens         INTEGER NOT NULL,
    cache_read_tokens     INTEGER NOT NULL,
    cache_write_tokens    INTEGER NOT NULL,
    uncached_input_cost   INTEGER NOT NULL,
    output_cost           INTEGER NOT NULL,
    cache_read_cost       INTEGER NOT NULL,
    cache_write_cost      INTEGER NOT NULL,
    total_cost            INTEGER NOT NULL,
    PRIMARY KEY (day, grain_key),
    CONSTRAINT token_spend_daily_one_user CHECK ((user_id IS NULL) <> (user_sub IS NULL)),
    CONSTRAINT token_spend_daily_one_model CHECK ((model_config_id IS NULL) <> (model_name IS NULL)),
    CONSTRAINT token_spend_daily_agent_pair CHECK (agent_id IS NULL OR agent_name IS NULL),
    CONSTRAINT token_spend_daily_workflow_pair CHECK (workflow_id IS NULL OR workflow_name IS NULL),
    CONSTRAINT token_spend_daily_root CHECK (
        (root_id IS NULL OR root_name IS NULL)
        AND (root_kind IS NULL) = (root_id IS NULL AND root_name IS NULL)),
    CONSTRAINT token_spend_daily_root_node CHECK (
        root_node_id IS NULL OR (root_id IS NULL) <> (root_name IS NULL)),
    CONSTRAINT token_spend_daily_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_calls CHECK (calls >= 1 AND unpriced_calls BETWEEN 0 AND calls),
    CONSTRAINT token_spend_daily_tokens_non_negative CHECK (
        uncached_input_tokens >= 0 AND output_tokens >= 0
        AND cache_read_tokens >= 0 AND cache_write_tokens >= 0),
    CONSTRAINT token_spend_daily_costs_non_negative CHECK (
        uncached_input_cost >= 0 AND output_cost >= 0
        AND cache_read_cost >= 0 AND cache_write_cost >= 0 AND total_cost >= 0)
);

CREATE INDEX IF NOT EXISTS idx_token_spend_daily_user
    ON token_spend_daily (user_id, day) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_user_sub
    ON token_spend_daily (user_sub, day) WHERE user_sub IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_agent
    ON token_spend_daily (agent_id, day) WHERE agent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_agent_name
    ON token_spend_daily (agent_name, day) WHERE agent_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_root
    ON token_spend_daily (root_id, day) WHERE root_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_root_name
    ON token_spend_daily (root_name, day) WHERE root_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_model
    ON token_spend_daily (model_config_id, day) WHERE model_config_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_model_name
    ON token_spend_daily (model_name, day) WHERE model_name IS NOT NULL;

CREATE TABLE IF NOT EXISTS token_spend_daily_activities (
    day             INTEGER NOT NULL,
    grouping        TEXT NOT NULL,
    grain_key       TEXT NOT NULL,
    user_id         TEXT,
    user_sub        TEXT,
    root_kind       TEXT,
    root_id         TEXT,
    root_name       TEXT,
    agent_id        TEXT,
    agent_name      TEXT,
    model_config_id TEXT,
    model_name      TEXT,
    activities      INTEGER NOT NULL,
    PRIMARY KEY (day, grouping, grain_key),
    CONSTRAINT token_spend_daily_activities_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_activities_grouping CHECK (grouping IN ('root', 'agent', 'model')),
    CONSTRAINT token_spend_daily_activities_one_user CHECK ((user_id IS NULL) <> (user_sub IS NULL)),
    CONSTRAINT token_spend_daily_activities_root CHECK (
        (root_id IS NULL OR root_name IS NULL)
        AND (root_kind IS NULL) = (root_id IS NULL AND root_name IS NULL)
        AND (grouping = 'root' OR root_kind IS NULL)),
    CONSTRAINT token_spend_daily_activities_agent CHECK (
        (agent_id IS NULL OR agent_name IS NULL)
        AND (grouping = 'agent' OR (agent_id IS NULL AND agent_name IS NULL))),
    CONSTRAINT token_spend_daily_activities_model CHECK (
        (model_config_id IS NULL OR model_name IS NULL)
        AND (grouping = 'model') = (model_config_id IS NOT NULL OR model_name IS NOT NULL)),
    CONSTRAINT token_spend_daily_activities_positive CHECK (activities > 0)
);

CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_user
    ON token_spend_daily_activities (user_id, day) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_user_sub
    ON token_spend_daily_activities (user_sub, day) WHERE user_sub IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_root
    ON token_spend_daily_activities (root_id, day) WHERE root_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_root_name
    ON token_spend_daily_activities (root_name, day) WHERE root_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_agent
    ON token_spend_daily_activities (agent_id, day) WHERE agent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_agent_name
    ON token_spend_daily_activities (agent_name, day) WHERE agent_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_model
    ON token_spend_daily_activities (model_config_id, day) WHERE model_config_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_model_name
    ON token_spend_daily_activities (model_name, day) WHERE model_name IS NOT NULL;

CREATE TABLE IF NOT EXISTS token_spend_daily_models (
    day                     INTEGER NOT NULL,
    grain_key               TEXT NOT NULL,
    model_config_id         TEXT,
    model_name              TEXT,
    resolved_model_name     TEXT,
    resolved_model_provider TEXT,
    calls                   INTEGER NOT NULL,
    unpriced_calls          INTEGER NOT NULL,
    uncached_input_tokens   INTEGER NOT NULL,
    output_tokens           INTEGER NOT NULL,
    cache_read_tokens       INTEGER NOT NULL,
    cache_write_tokens      INTEGER NOT NULL,
    uncached_input_cost     INTEGER NOT NULL,
    output_cost             INTEGER NOT NULL,
    cache_read_cost         INTEGER NOT NULL,
    cache_write_cost        INTEGER NOT NULL,
    total_cost              INTEGER NOT NULL,
    activities              INTEGER NOT NULL,
    PRIMARY KEY (day, grain_key),
    CONSTRAINT token_spend_daily_models_one_model CHECK ((model_config_id IS NULL) <> (model_name IS NULL)),
    CONSTRAINT token_spend_daily_models_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_models_calls CHECK (calls >= 1 AND unpriced_calls BETWEEN 0 AND calls),
    CONSTRAINT token_spend_daily_models_tokens_non_negative CHECK (
        uncached_input_tokens >= 0 AND output_tokens >= 0
        AND cache_read_tokens >= 0 AND cache_write_tokens >= 0),
    CONSTRAINT token_spend_daily_models_costs_non_negative CHECK (
        uncached_input_cost >= 0 AND output_cost >= 0
        AND cache_read_cost >= 0 AND cache_write_cost >= 0 AND total_cost >= 0),
    CONSTRAINT token_spend_daily_models_activities CHECK (activities BETWEEN 0 AND calls)
);

CREATE TABLE IF NOT EXISTS token_spend_rollup_state (
    id              SMALLINT NOT NULL PRIMARY KEY,
    grain_version   INTEGER NOT NULL,
    refreshed_time  INTEGER,
    reconciled_time INTEGER,
    CONSTRAINT token_spend_rollup_state_singleton CHECK (id = 1)
);

-- +goose Down
DROP TABLE IF EXISTS token_spend_rollup_state;
DROP TABLE IF EXISTS token_spend_daily_models;
DROP TABLE IF EXISTS token_spend_daily_activities;
DROP TABLE IF EXISTS token_spend_daily;
