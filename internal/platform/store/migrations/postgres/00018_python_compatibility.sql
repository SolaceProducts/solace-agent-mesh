-- +goose Up

ALTER TABLE agents ADD COLUMN IF NOT EXISTS skill_refs TEXT;
ALTER TABLE connectors ADD COLUMN IF NOT EXISTS resource_key TEXT;

CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT,
    description TEXT,
    app_config TEXT NOT NULL,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_deployments (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    action TEXT NOT NULL,
    workflow_config_snapshot TEXT,
    error_message TEXT,
    deployer_version TEXT,
    created_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS skills (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    tags TEXT,
    origin TEXT NOT NULL DEFAULT 'filesystem',
    status TEXT NOT NULL DEFAULT 'available',
    has_references INTEGER NOT NULL DEFAULT 0,
    has_assets INTEGER NOT NULL DEFAULT 0,
    has_tools INTEGER NOT NULL DEFAULT 0,
    resource_count INTEGER NOT NULL DEFAULT 0,
    bundled_tools TEXT,
    config_yaml TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS tools (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    origin TEXT NOT NULL DEFAULT 'filesystem',
    status TEXT NOT NULL DEFAULT 'available',
    parameters TEXT,
    instructions TEXT,
    artifact_params TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables
             WHERE table_schema='public' AND table_name='alembic_version_enterprise') THEN
    UPDATE agent_deployments
      SET status = 'failed',
          error_message = 'Reset during Python-to-Go migration. Redeploy required.'
      WHERE status IN ('success', 'in_progress', 'pending');
    UPDATE gateway_deployments
      SET status = 'failed',
          error_message = 'Reset during Python-to-Go migration. Redeploy required.'
      WHERE status IN ('success', 'in_progress', 'pending');
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
