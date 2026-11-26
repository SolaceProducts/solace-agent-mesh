-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION _to_uuid_pkg(val TEXT) RETURNS UUID AS $$
DECLARE
    hex32 TEXT;
BEGIN
    IF val IS NULL THEN RETURN NULL; END IF;

    IF val ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN
        RETURN val::uuid;
    END IF;

    hex32 := val;
    IF hex32 ~ '^[0-9a-fA-F]{32}$' THEN
        RETURN (
            substring(hex32, 1, 8) || '-' ||
            substring(hex32, 9, 4) || '-' ||
            substring(hex32, 13, 4) || '-' ||
            substring(hex32, 17, 4) || '-' ||
            substring(hex32, 21, 12)
        )::uuid;
    END IF;

    RETURN md5(val)::uuid;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

ALTER TABLE tool_package_files DROP CONSTRAINT tool_package_files_package_id_fkey;
ALTER TABLE agent_toolset_configs DROP CONSTRAINT agent_toolset_configs_package_id_fkey;

UPDATE tool_packages SET id = _to_uuid_pkg(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE tool_package_files SET id = _to_uuid_pkg(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';
UPDATE tool_package_files SET package_id = _to_uuid_pkg(package_id)::text
WHERE package_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE agent_toolset_configs SET package_id = _to_uuid_pkg(package_id)::text
WHERE package_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

ALTER TABLE tool_packages ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE tool_package_files ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE tool_package_files ALTER COLUMN package_id TYPE UUID USING package_id::uuid;
ALTER TABLE agent_toolset_configs ALTER COLUMN package_id TYPE UUID USING package_id::uuid;

ALTER TABLE tool_package_files ADD CONSTRAINT tool_package_files_package_id_fkey
    FOREIGN KEY (package_id) REFERENCES tool_packages(id) ON DELETE CASCADE;
ALTER TABLE agent_toolset_configs ADD CONSTRAINT agent_toolset_configs_package_id_fkey
    FOREIGN KEY (package_id) REFERENCES tool_packages(id) ON DELETE CASCADE;

DROP FUNCTION _to_uuid_pkg(TEXT);

-- +goose Down

ALTER TABLE tool_package_files DROP CONSTRAINT IF EXISTS tool_package_files_package_id_fkey;
ALTER TABLE agent_toolset_configs DROP CONSTRAINT IF EXISTS agent_toolset_configs_package_id_fkey;

ALTER TABLE tool_packages ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE tool_package_files ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE tool_package_files ALTER COLUMN package_id TYPE TEXT USING package_id::text;
ALTER TABLE agent_toolset_configs ALTER COLUMN package_id TYPE TEXT USING package_id::text;

ALTER TABLE tool_package_files ADD CONSTRAINT tool_package_files_package_id_fkey
    FOREIGN KEY (package_id) REFERENCES tool_packages(id) ON DELETE CASCADE;
ALTER TABLE agent_toolset_configs ADD CONSTRAINT agent_toolset_configs_package_id_fkey
    FOREIGN KEY (package_id) REFERENCES tool_packages(id) ON DELETE CASCADE;
