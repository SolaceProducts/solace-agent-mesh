
-- +goose Up

ALTER TABLE rbac_roles ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0;

INSERT INTO rbac_roles (id, name, description, created_by, updated_by, created_time, updated_time, builtin)
VALUES ('00000000-0000-0000-0000-000000000001', 'sam_manager',
        'SAM Manager — administer everything in SAM except RBAC management.',
        'system', 'system', 1784678400000, 1784678400000, 1);

INSERT INTO rbac_role_scopes (role_id, scope)
SELECT (SELECT id FROM rbac_roles WHERE name = 'sam_manager'), column1
FROM (VALUES
    ('agent_builder:*:*'),
    ('connector:*:*'),
    ('entrypoint:*:*'),
    ('skill:*:*'),
    ('toolset:*:*'),
    ('model_config:*:*'),
    ('workflow_builder:*:*'),
    ('tool:*:*'),
    ('evaluation:_:*'),
    ('deployment:_:*'),
    ('analytics:_:*'),
    ('profile_provider:_:*'),
    ('builder:_:*'),
    ('notify:_:*'),
    ('agent:*:*'),
    ('workflow:*:*')
) AS scopes;

-- +goose Down

DELETE FROM rbac_roles WHERE id = '00000000-0000-0000-0000-000000000001';
ALTER TABLE rbac_roles DROP COLUMN builtin;
