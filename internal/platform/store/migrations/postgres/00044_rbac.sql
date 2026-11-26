
-- +goose Up
CREATE TABLE IF NOT EXISTS rbac_roles (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS rbac_role_scopes (
    role_id         UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    scope           TEXT NOT NULL,
    PRIMARY KEY (role_id, scope)
);

CREATE TABLE IF NOT EXISTS rbac_role_inheritance (
    role_id         UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    parent_role_id  UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, parent_role_id),
    CHECK (role_id <> parent_role_id)
);

CREATE INDEX IF NOT EXISTS ix_rbac_role_inheritance_parent
    ON rbac_role_inheritance(parent_role_id);

CREATE TABLE IF NOT EXISTS rbac_user_role_assignments (
    id              UUID PRIMARY KEY,
    identity        TEXT NOT NULL,
    gateway_id      TEXT NOT NULL DEFAULT '',
    role_id         UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL,
    UNIQUE (identity, gateway_id, role_id)
);

CREATE INDEX IF NOT EXISTS ix_rbac_user_role_assignments_identity
    ON rbac_user_role_assignments(identity);
CREATE INDEX IF NOT EXISTS ix_rbac_user_role_assignments_role
    ON rbac_user_role_assignments(role_id);

CREATE TABLE IF NOT EXISTS rbac_idp_claim_mappings (
    id              UUID PRIMARY KEY,
    oidc_provider   TEXT NOT NULL,
    claim_key       TEXT NOT NULL,
    claim_value     TEXT NOT NULL,
    role_id         UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL,
    UNIQUE (oidc_provider, claim_key, claim_value, role_id)
);

CREATE INDEX IF NOT EXISTS ix_rbac_idp_claim_mappings_lookup
    ON rbac_idp_claim_mappings(oidc_provider, claim_key, claim_value);
CREATE INDEX IF NOT EXISTS ix_rbac_idp_claim_mappings_role
    ON rbac_idp_claim_mappings(role_id);

CREATE TABLE IF NOT EXISTS rbac_default_roles (
    role_id         UUID PRIMARY KEY REFERENCES rbac_roles(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS rbac_default_roles;
DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_role;
DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_lookup;
DROP TABLE IF EXISTS rbac_idp_claim_mappings;
DROP INDEX IF EXISTS ix_rbac_user_role_assignments_role;
DROP INDEX IF EXISTS ix_rbac_user_role_assignments_identity;
DROP TABLE IF EXISTS rbac_user_role_assignments;
DROP INDEX IF EXISTS ix_rbac_role_inheritance_parent;
DROP TABLE IF EXISTS rbac_role_inheritance;
DROP TABLE IF EXISTS rbac_role_scopes;
DROP TABLE IF EXISTS rbac_roles;
