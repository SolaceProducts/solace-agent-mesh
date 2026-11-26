
-- +goose Up
DELETE FROM rbac_role_scopes
 WHERE role_id = '00000000-0000-0000-0000-000000000001'
   AND scope IN ('agent:*:*', 'workflow:*:*', 'analytics:*:*', 'notify:*:*');

INSERT INTO rbac_role_scopes (role_id, scope)
SELECT '00000000-0000-0000-0000-000000000001', scopes.scope
  FROM (SELECT 'agent:*:invoke' AS scope
        UNION ALL SELECT 'workflow:*:invoke'
        UNION ALL SELECT 'agent:*:read_feedback'
        UNION ALL SELECT 'workflow:*:read_feedback'
        UNION ALL SELECT 'analytics:*:read'
        UNION ALL SELECT 'notify:*:send'
        UNION ALL SELECT 'notify:*:send_others'
        UNION ALL SELECT '*:*:read_usage'
        UNION ALL SELECT 'budget:*:*') AS scopes
 WHERE EXISTS (SELECT 1 FROM rbac_roles WHERE id = '00000000-0000-0000-0000-000000000001')
   AND NOT EXISTS (SELECT 1 FROM rbac_role_scopes
                    WHERE role_id = '00000000-0000-0000-0000-000000000001'
                      AND scope = scopes.scope);

-- +goose Down
DELETE FROM rbac_role_scopes
 WHERE role_id = '00000000-0000-0000-0000-000000000001'
   AND scope IN ('agent:*:invoke', 'workflow:*:invoke', 'agent:*:read_feedback',
                 'workflow:*:read_feedback', 'analytics:*:read', 'notify:*:send',
                 'notify:*:send_others', '*:*:read_usage', 'budget:*:*');

INSERT INTO rbac_role_scopes (role_id, scope)
SELECT '00000000-0000-0000-0000-000000000001', scopes.scope
  FROM (SELECT 'agent:*:*' AS scope
        UNION ALL SELECT 'workflow:*:*'
        UNION ALL SELECT 'analytics:*:*'
        UNION ALL SELECT 'notify:*:*') AS scopes
 WHERE EXISTS (SELECT 1 FROM rbac_roles WHERE id = '00000000-0000-0000-0000-000000000001')
   AND NOT EXISTS (SELECT 1 FROM rbac_role_scopes
                    WHERE role_id = '00000000-0000-0000-0000-000000000001'
                      AND scope = scopes.scope);
