
-- +goose Up
-- +goose StatementBegin
ALTER TABLE rbac_idp_claim_mappings ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS rbac_idp_claim_mapping_roles (
    claim_mapping_id UUID NOT NULL REFERENCES rbac_idp_claim_mappings(id) ON DELETE CASCADE,
    role_id          UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    PRIMARY KEY (claim_mapping_id, role_id)
);
CREATE INDEX IF NOT EXISTS ix_rbac_idp_claim_mapping_roles_role
    ON rbac_idp_claim_mapping_roles(role_id);

INSERT INTO rbac_idp_claim_mapping_roles (claim_mapping_id, role_id)
SELECT canon.min_id, o.role_id
FROM rbac_idp_claim_mappings o
JOIN (
    SELECT oidc_provider, claim_value, MIN(id::text)::uuid AS min_id
    FROM rbac_idp_claim_mappings
    GROUP BY oidc_provider, claim_value
) canon ON canon.oidc_provider = o.oidc_provider AND canon.claim_value = o.claim_value
JOIN rbac_idp_claim_mappings ck ON ck.id = canon.min_id
WHERE o.claim_key = ck.claim_key
ON CONFLICT DO NOTHING;

WITH canon AS (
    SELECT oidc_provider, claim_value, MIN(id::text)::uuid AS min_id
    FROM rbac_idp_claim_mappings
    GROUP BY oidc_provider, claim_value
)
DELETE FROM rbac_idp_claim_mappings
WHERE id NOT IN (SELECT min_id FROM canon);

DO $$
DECLARE c text;
BEGIN
    SELECT conname INTO c FROM pg_constraint
    WHERE conrelid = 'rbac_idp_claim_mappings'::regclass AND contype = 'u';
    IF c IS NOT NULL THEN
        EXECUTE 'ALTER TABLE rbac_idp_claim_mappings DROP CONSTRAINT ' || quote_ident(c);
    END IF;
END $$;
DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_lookup;
ALTER TABLE rbac_idp_claim_mappings DROP COLUMN IF EXISTS role_id;

ALTER TABLE rbac_idp_claim_mappings
    ADD CONSTRAINT rbac_idp_claim_mappings_provider_value_key UNIQUE (oidc_provider, claim_value);
CREATE INDEX IF NOT EXISTS ix_rbac_idp_claim_mappings_lookup
    ON rbac_idp_claim_mappings(oidc_provider, claim_value);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE rbac_idp_claim_mappings ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES rbac_roles(id) ON DELETE CASCADE;

CREATE TEMP TABLE _legacy_cm AS
SELECT (md5(random()::text || clock_timestamp()::text))::uuid AS id,
       m.oidc_provider, m.claim_key, m.claim_value, r.role_id,
       m.created_by, m.updated_by, m.created_time, m.updated_time
FROM rbac_idp_claim_mappings m
JOIN rbac_idp_claim_mapping_roles r ON r.claim_mapping_id = m.id;

DROP INDEX IF EXISTS ix_rbac_idp_claim_mapping_roles_role;
DROP TABLE IF EXISTS rbac_idp_claim_mapping_roles;
DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_lookup;
DO $$
DECLARE c text;
BEGIN
    SELECT conname INTO c FROM pg_constraint
    WHERE conrelid = 'rbac_idp_claim_mappings'::regclass AND contype = 'u';
    IF c IS NOT NULL THEN
        EXECUTE 'ALTER TABLE rbac_idp_claim_mappings DROP CONSTRAINT ' || quote_ident(c);
    END IF;
END $$;
ALTER TABLE rbac_idp_claim_mappings DROP COLUMN IF EXISTS name;

TRUNCATE TABLE rbac_idp_claim_mappings;
INSERT INTO rbac_idp_claim_mappings
    (id, oidc_provider, claim_key, claim_value, role_id, created_by, updated_by, created_time, updated_time)
SELECT id, oidc_provider, claim_key, claim_value, role_id, created_by, updated_by, created_time, updated_time
FROM _legacy_cm;
DROP TABLE _legacy_cm;

ALTER TABLE rbac_idp_claim_mappings ALTER COLUMN role_id SET NOT NULL;
ALTER TABLE rbac_idp_claim_mappings
    ADD CONSTRAINT rbac_idp_claim_mappings_provider_key_value_role_key
    UNIQUE (oidc_provider, claim_key, claim_value, role_id);
CREATE INDEX IF NOT EXISTS ix_rbac_idp_claim_mappings_lookup
    ON rbac_idp_claim_mappings(oidc_provider, claim_key, claim_value);
CREATE INDEX IF NOT EXISTS ix_rbac_idp_claim_mappings_role
    ON rbac_idp_claim_mappings(role_id);
-- +goose StatementEnd
