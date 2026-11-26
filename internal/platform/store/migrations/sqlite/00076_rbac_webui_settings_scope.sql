
-- +goose Up
INSERT INTO rbac_role_scopes (role_id, scope)
SELECT '00000000-0000-0000-0000-000000000001', 'webui_settings:*:*'
 WHERE EXISTS (SELECT 1 FROM rbac_roles WHERE id = '00000000-0000-0000-0000-000000000001');

-- +goose Down
DELETE FROM rbac_role_scopes
 WHERE role_id = '00000000-0000-0000-0000-000000000001'
   AND scope = 'webui_settings:*:*';
