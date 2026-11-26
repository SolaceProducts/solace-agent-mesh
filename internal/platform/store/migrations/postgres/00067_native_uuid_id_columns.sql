-- +goose Up

ALTER TABLE eval_agent_watchlist ALTER COLUMN id TYPE uuid USING id::uuid;

ALTER TABLE platform_identity_config ALTER COLUMN toolset_id TYPE uuid USING toolset_id::uuid;

UPDATE platform_identity_config
   SET toolset_id = NULL
 WHERE toolset_id IS NOT NULL
   AND toolset_id NOT IN (SELECT id FROM tool_packages);

ALTER TABLE platform_identity_config
    ADD CONSTRAINT platform_identity_config_toolset_id_fkey
    FOREIGN KEY (toolset_id) REFERENCES tool_packages (id) ON DELETE SET NULL;

-- +goose Down

ALTER TABLE platform_identity_config DROP CONSTRAINT IF EXISTS platform_identity_config_toolset_id_fkey;
ALTER TABLE platform_identity_config ALTER COLUMN toolset_id TYPE text USING toolset_id::text;
ALTER TABLE eval_agent_watchlist ALTER COLUMN id TYPE text USING id::text;
