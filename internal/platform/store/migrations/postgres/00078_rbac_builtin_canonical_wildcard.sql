
-- +goose Up

UPDATE rbac_role_scopes
   SET scope = REPLACE(scope, ':_:', ':*:')
 WHERE role_id = '00000000-0000-0000-0000-000000000001'
   AND scope <> REPLACE(scope, ':_:', ':*:');

-- +goose Down

UPDATE rbac_role_scopes
   SET scope = REPLACE(scope, ':*:', ':_:')
 WHERE role_id = '00000000-0000-0000-0000-000000000001'
   AND scope IN ('evaluation:*:*', 'deployment:*:*', 'analytics:*:*',
                 'profile_provider:*:*', 'builder:*:*', 'notify:*:*');
