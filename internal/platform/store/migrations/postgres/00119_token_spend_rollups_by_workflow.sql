
-- +goose Up
DROP TABLE IF EXISTS token_spend_daily_activities;
DROP TABLE IF EXISTS token_spend_daily;

CREATE TABLE IF NOT EXISTS token_spend_daily (
    day                   BIGINT NOT NULL,
    grain_key             TEXT NOT NULL,
    user_id               UUID,
    user_sub              TEXT,
    agent_id              UUID,
    agent_name            TEXT,
    workflow_id           UUID,
    workflow_name         TEXT,
    root_kind             TEXT,
    root_id               UUID,
    root_name             TEXT,
    root_node_id          TEXT,
    model_config_id       UUID,
    model_name            TEXT,
    user_label            TEXT,
    agent_label           TEXT,
    workflow_label        TEXT,
    root_label            TEXT,
    model_label           TEXT,
    calls                 BIGINT NOT NULL,
    unpriced_calls        BIGINT NOT NULL,
    uncached_input_tokens BIGINT NOT NULL,
    output_tokens         BIGINT NOT NULL,
    cache_read_tokens     BIGINT NOT NULL,
    cache_write_tokens    BIGINT NOT NULL,
    uncached_input_cost   BIGINT NOT NULL,
    output_cost           BIGINT NOT NULL,
    cache_read_cost       BIGINT NOT NULL,
    cache_write_cost      BIGINT NOT NULL,
    total_cost            BIGINT NOT NULL,
    PRIMARY KEY (day, grain_key),
    CONSTRAINT token_spend_daily_one_user CHECK (num_nonnulls(user_id, user_sub) = 1),
    CONSTRAINT token_spend_daily_one_model CHECK (num_nonnulls(model_config_id, model_name) = 1),
    CONSTRAINT token_spend_daily_agent_pair CHECK (num_nonnulls(agent_id, agent_name) <= 1),
    CONSTRAINT token_spend_daily_workflow_pair CHECK (num_nonnulls(workflow_id, workflow_name) <= 1),
    CONSTRAINT token_spend_daily_root CHECK (
        num_nonnulls(root_id, root_name) <= 1
        AND (root_kind IS NULL) = (num_nonnulls(root_id, root_name) = 0)),
    CONSTRAINT token_spend_daily_root_node CHECK (
        root_node_id IS NULL OR num_nonnulls(root_id, root_name) = 1),
    CONSTRAINT token_spend_daily_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_calls CHECK (calls >= 1 AND unpriced_calls BETWEEN 0 AND calls),
    CONSTRAINT token_spend_daily_tokens_non_negative CHECK (
        uncached_input_tokens >= 0 AND output_tokens >= 0
        AND cache_read_tokens >= 0 AND cache_write_tokens >= 0),
    CONSTRAINT token_spend_daily_costs_non_negative CHECK (
        uncached_input_cost >= 0 AND output_cost >= 0
        AND cache_read_cost >= 0 AND cache_write_cost >= 0 AND total_cost >= 0)
) WITH (fillfactor = 80);

CREATE INDEX IF NOT EXISTS idx_token_spend_daily_user
    ON token_spend_daily (user_id, day) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_user_sub
    ON token_spend_daily (user_sub, day) WHERE user_sub IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_agent
    ON token_spend_daily (agent_id, day) WHERE agent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_agent_name
    ON token_spend_daily (agent_name, day) WHERE agent_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_workflow
    ON token_spend_daily (workflow_id, day) WHERE workflow_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_workflow_name
    ON token_spend_daily (workflow_name, day) WHERE workflow_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_root
    ON token_spend_daily (root_id, day) WHERE root_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_root_name
    ON token_spend_daily (root_name, day) WHERE root_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_model
    ON token_spend_daily (model_config_id, day) WHERE model_config_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_model_name
    ON token_spend_daily (model_name, day) WHERE model_name IS NOT NULL;

CREATE TABLE IF NOT EXISTS token_spend_daily_activities (
    day             BIGINT NOT NULL,
    grouping        TEXT NOT NULL,
    grain_key       TEXT NOT NULL,
    user_id         UUID,
    user_sub        TEXT,
    root_kind       TEXT,
    root_id         UUID,
    root_name       TEXT,
    agent_id        UUID,
    agent_name      TEXT,
    workflow_id     UUID,
    workflow_name   TEXT,
    model_config_id UUID,
    model_name      TEXT,
    activities      BIGINT NOT NULL,
    PRIMARY KEY (day, grouping, grain_key),
    CONSTRAINT token_spend_daily_activities_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_activities_grouping CHECK (grouping IN ('root', 'agent', 'workflow', 'model')),
    CONSTRAINT token_spend_daily_activities_one_user CHECK (num_nonnulls(user_id, user_sub) = 1),
    CONSTRAINT token_spend_daily_activities_root CHECK (
        num_nonnulls(root_id, root_name) <= 1
        AND (root_kind IS NULL) = (num_nonnulls(root_id, root_name) = 0)
        AND (grouping = 'root' OR root_kind IS NULL)),
    CONSTRAINT token_spend_daily_activities_agent CHECK (
        num_nonnulls(agent_id, agent_name) <= 1
        AND (grouping = 'agent' OR num_nonnulls(agent_id, agent_name) = 0)),
    CONSTRAINT token_spend_daily_activities_workflow CHECK (
        (grouping = 'workflow') = (num_nonnulls(workflow_id, workflow_name) = 1)
        AND num_nonnulls(workflow_id, workflow_name) <= 1),
    CONSTRAINT token_spend_daily_activities_model CHECK (
        (grouping = 'model') = (num_nonnulls(model_config_id, model_name) = 1)
        AND num_nonnulls(model_config_id, model_name) <= 1),
    CONSTRAINT token_spend_daily_activities_positive CHECK (activities > 0)
) WITH (fillfactor = 80);

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
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_workflow
    ON token_spend_daily_activities (workflow_id, day) WHERE workflow_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_workflow_name
    ON token_spend_daily_activities (workflow_name, day) WHERE workflow_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_model
    ON token_spend_daily_activities (model_config_id, day) WHERE model_config_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_token_spend_daily_activities_model_name
    ON token_spend_daily_activities (model_name, day) WHERE model_name IS NOT NULL;

UPDATE token_spend_rollup_state SET grain_version = 2, refreshed_time = NULL, reconciled_time = NULL WHERE id = 1;

-- +goose Down
DROP TABLE IF EXISTS token_spend_daily_activities;
DROP TABLE IF EXISTS token_spend_daily;

CREATE TABLE IF NOT EXISTS token_spend_daily (
    day                   BIGINT NOT NULL,
    grain_key             TEXT NOT NULL,
    user_id               UUID,
    user_sub              TEXT,
    agent_id              UUID,
    agent_name            TEXT,
    workflow_id           UUID,
    workflow_name         TEXT,
    root_kind             TEXT,
    root_id               UUID,
    root_name             TEXT,
    root_node_id          TEXT,
    model_config_id       UUID,
    model_name            TEXT,
    user_label            TEXT,
    agent_label           TEXT,
    root_label            TEXT,
    model_label           TEXT,
    calls                 BIGINT NOT NULL,
    unpriced_calls        BIGINT NOT NULL,
    uncached_input_tokens BIGINT NOT NULL,
    output_tokens         BIGINT NOT NULL,
    cache_read_tokens     BIGINT NOT NULL,
    cache_write_tokens    BIGINT NOT NULL,
    uncached_input_cost   BIGINT NOT NULL,
    output_cost           BIGINT NOT NULL,
    cache_read_cost       BIGINT NOT NULL,
    cache_write_cost      BIGINT NOT NULL,
    total_cost            BIGINT NOT NULL,
    PRIMARY KEY (day, grain_key),
    CONSTRAINT token_spend_daily_one_user CHECK (num_nonnulls(user_id, user_sub) = 1),
    CONSTRAINT token_spend_daily_one_model CHECK (num_nonnulls(model_config_id, model_name) = 1),
    CONSTRAINT token_spend_daily_agent_pair CHECK (num_nonnulls(agent_id, agent_name) <= 1),
    CONSTRAINT token_spend_daily_workflow_pair CHECK (num_nonnulls(workflow_id, workflow_name) <= 1),
    CONSTRAINT token_spend_daily_root CHECK (
        num_nonnulls(root_id, root_name) <= 1
        AND (root_kind IS NULL) = (num_nonnulls(root_id, root_name) = 0)),
    CONSTRAINT token_spend_daily_root_node CHECK (
        root_node_id IS NULL OR num_nonnulls(root_id, root_name) = 1),
    CONSTRAINT token_spend_daily_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_calls CHECK (calls >= 1 AND unpriced_calls BETWEEN 0 AND calls),
    CONSTRAINT token_spend_daily_tokens_non_negative CHECK (
        uncached_input_tokens >= 0 AND output_tokens >= 0
        AND cache_read_tokens >= 0 AND cache_write_tokens >= 0),
    CONSTRAINT token_spend_daily_costs_non_negative CHECK (
        uncached_input_cost >= 0 AND output_cost >= 0
        AND cache_read_cost >= 0 AND cache_write_cost >= 0 AND total_cost >= 0)
) WITH (fillfactor = 80);

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
    day             BIGINT NOT NULL,
    grouping        TEXT NOT NULL,
    grain_key       TEXT NOT NULL,
    user_id         UUID,
    user_sub        TEXT,
    root_kind       TEXT,
    root_id         UUID,
    root_name       TEXT,
    agent_id        UUID,
    agent_name      TEXT,
    model_config_id UUID,
    model_name      TEXT,
    activities      BIGINT NOT NULL,
    PRIMARY KEY (day, grouping, grain_key),
    CONSTRAINT token_spend_daily_activities_utc_day CHECK (day >= 0 AND day % 86400000 = 0),
    CONSTRAINT token_spend_daily_activities_grouping CHECK (grouping IN ('root', 'agent', 'model')),
    CONSTRAINT token_spend_daily_activities_one_user CHECK (num_nonnulls(user_id, user_sub) = 1),
    CONSTRAINT token_spend_daily_activities_root CHECK (
        num_nonnulls(root_id, root_name) <= 1
        AND (root_kind IS NULL) = (num_nonnulls(root_id, root_name) = 0)
        AND (grouping = 'root' OR root_kind IS NULL)),
    CONSTRAINT token_spend_daily_activities_agent CHECK (
        num_nonnulls(agent_id, agent_name) <= 1
        AND (grouping = 'agent' OR num_nonnulls(agent_id, agent_name) = 0)),
    CONSTRAINT token_spend_daily_activities_model CHECK (
        (grouping = 'model') = (num_nonnulls(model_config_id, model_name) = 1)
        AND num_nonnulls(model_config_id, model_name) <= 1),
    CONSTRAINT token_spend_daily_activities_positive CHECK (activities > 0)
) WITH (fillfactor = 80);

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

DELETE FROM token_spend_rollup_state WHERE id = 1;
