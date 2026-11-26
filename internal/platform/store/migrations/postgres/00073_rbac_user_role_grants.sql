
-- +goose Up
CREATE TABLE IF NOT EXISTS rbac_user_roles (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id         UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    subject_type    TEXT NOT NULL DEFAULT '',
    subject_value   TEXT NOT NULL DEFAULT '',
    issuer          TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL,
    UNIQUE (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS ix_rbac_user_roles_user ON rbac_user_roles(user_id);
CREATE INDEX IF NOT EXISTS ix_rbac_user_roles_role ON rbac_user_roles(role_id);

CREATE TABLE IF NOT EXISTS rbac_pending_user_roles (
    id             UUID PRIMARY KEY,
    subject_type   TEXT NOT NULL CHECK (subject_type IN ('email', 'sub', 'legacy')),
    subject_value  TEXT NOT NULL,
    issuer         TEXT NOT NULL DEFAULT '',
    role_id        UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    created_by     TEXT NOT NULL,
    updated_by     TEXT NOT NULL,
    created_time   BIGINT NOT NULL,
    updated_time   BIGINT NOT NULL,
    CHECK (subject_type <> 'sub' OR issuer <> ''),
    UNIQUE (subject_type, subject_value, issuer, role_id)
);

CREATE INDEX IF NOT EXISTS ix_rbac_pending_user_roles_match
    ON rbac_pending_user_roles(subject_type, subject_value);
CREATE INDEX IF NOT EXISTS ix_rbac_pending_user_roles_role
    ON rbac_pending_user_roles(role_id);

INSERT INTO rbac_user_roles (id, user_id, role_id, created_by, updated_by, created_time, updated_time)
SELECT gen_random_uuid(), u.id, a.role_id, a.created_by, a.updated_by, a.created_time, a.updated_time
FROM rbac_user_role_assignments a
JOIN users u ON (lower(u.email) = lower(a.identity) OR u.sub = a.identity)
ON CONFLICT (user_id, role_id) DO NOTHING;

INSERT INTO rbac_pending_user_roles (id, subject_type, subject_value, issuer, role_id, created_by, updated_by, created_time, updated_time)
SELECT gen_random_uuid(), 'legacy',
       CASE WHEN a.identity LIKE '%@%' THEN lower(a.identity) ELSE a.identity END,
       '', a.role_id, a.created_by, a.updated_by, a.created_time, a.updated_time
FROM rbac_user_role_assignments a
WHERE NOT EXISTS (SELECT 1 FROM users u WHERE lower(u.email) = lower(a.identity) OR u.sub = a.identity)
ON CONFLICT (subject_type, subject_value, issuer, role_id) DO NOTHING;

DROP TABLE rbac_user_role_assignments;

-- +goose Down
CREATE TABLE IF NOT EXISTS rbac_user_role_assignments (
    id           UUID PRIMARY KEY,
    identity     TEXT NOT NULL,
    gateway_id   TEXT NOT NULL DEFAULT '',
    role_id      UUID NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    created_by   TEXT NOT NULL,
    updated_by   TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,
    UNIQUE (identity, gateway_id, role_id)
);

DROP INDEX IF EXISTS ix_rbac_pending_user_roles_role;
DROP INDEX IF EXISTS ix_rbac_pending_user_roles_match;
DROP TABLE IF EXISTS rbac_pending_user_roles;

DROP INDEX IF EXISTS ix_rbac_user_roles_role;
DROP INDEX IF EXISTS ix_rbac_user_roles_user;
DROP TABLE IF EXISTS rbac_user_roles;
