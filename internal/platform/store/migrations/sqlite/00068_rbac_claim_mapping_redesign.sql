
-- +goose Up
-- +goose StatementBegin
CREATE TABLE rbac_idp_claim_mappings_new (
    id            TEXT PRIMARY KEY,
    oidc_provider TEXT NOT NULL,
    claim_key     TEXT NOT NULL,
    claim_value   TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL,
    updated_by    TEXT NOT NULL,
    created_time  INTEGER NOT NULL,
    updated_time  INTEGER NOT NULL,
    UNIQUE (oidc_provider, claim_value)
);

INSERT INTO rbac_idp_claim_mappings_new
    (id, oidc_provider, claim_key, claim_value, name, created_by, updated_by, created_time, updated_time)
SELECT o.id, o.oidc_provider, o.claim_key, o.claim_value, '',
       o.created_by, o.updated_by, o.created_time, o.updated_time
FROM rbac_idp_claim_mappings o
WHERE o.id = (SELECT MIN(o2.id) FROM rbac_idp_claim_mappings o2
              WHERE o2.oidc_provider = o.oidc_provider AND o2.claim_value = o.claim_value);

CREATE TABLE _cm_role_pairs (claim_mapping_id TEXT NOT NULL, role_id TEXT NOT NULL);
INSERT INTO _cm_role_pairs (claim_mapping_id, role_id)
SELECT canon.id, o.role_id
FROM rbac_idp_claim_mappings o
JOIN rbac_idp_claim_mappings_new canon
  ON canon.oidc_provider = o.oidc_provider AND canon.claim_value = o.claim_value
WHERE o.claim_key = canon.claim_key;

DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_role;
DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_lookup;
DROP TABLE rbac_idp_claim_mappings;
ALTER TABLE rbac_idp_claim_mappings_new RENAME TO rbac_idp_claim_mappings;
CREATE INDEX ix_rbac_idp_claim_mappings_lookup
    ON rbac_idp_claim_mappings(oidc_provider, claim_value);

CREATE TABLE rbac_idp_claim_mapping_roles (
    claim_mapping_id TEXT NOT NULL REFERENCES rbac_idp_claim_mappings(id) ON DELETE CASCADE,
    role_id          TEXT NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    PRIMARY KEY (claim_mapping_id, role_id)
);
INSERT OR IGNORE INTO rbac_idp_claim_mapping_roles (claim_mapping_id, role_id)
SELECT claim_mapping_id, role_id FROM _cm_role_pairs;
CREATE INDEX ix_rbac_idp_claim_mapping_roles_role
    ON rbac_idp_claim_mapping_roles(role_id);

DROP TABLE _cm_role_pairs;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE rbac_idp_claim_mappings_old (
    id            TEXT PRIMARY KEY,
    oidc_provider TEXT NOT NULL,
    claim_key     TEXT NOT NULL,
    claim_value   TEXT NOT NULL,
    role_id       TEXT NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    created_by    TEXT NOT NULL,
    updated_by    TEXT NOT NULL,
    created_time  INTEGER NOT NULL,
    updated_time  INTEGER NOT NULL,
    UNIQUE (oidc_provider, claim_key, claim_value, role_id)
);

INSERT INTO rbac_idp_claim_mappings_old
    (id, oidc_provider, claim_key, claim_value, role_id, created_by, updated_by, created_time, updated_time)
SELECT lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(6))),
       m.oidc_provider, m.claim_key, m.claim_value, r.role_id,
       m.created_by, m.updated_by, m.created_time, m.updated_time
FROM rbac_idp_claim_mappings m
JOIN rbac_idp_claim_mapping_roles r ON r.claim_mapping_id = m.id;

DROP INDEX IF EXISTS ix_rbac_idp_claim_mapping_roles_role;
DROP TABLE rbac_idp_claim_mapping_roles;
DROP INDEX IF EXISTS ix_rbac_idp_claim_mappings_lookup;
DROP TABLE rbac_idp_claim_mappings;
ALTER TABLE rbac_idp_claim_mappings_old RENAME TO rbac_idp_claim_mappings;
CREATE INDEX ix_rbac_idp_claim_mappings_lookup
    ON rbac_idp_claim_mappings(oidc_provider, claim_key, claim_value);
CREATE INDEX ix_rbac_idp_claim_mappings_role
    ON rbac_idp_claim_mappings(role_id);
-- +goose StatementEnd
