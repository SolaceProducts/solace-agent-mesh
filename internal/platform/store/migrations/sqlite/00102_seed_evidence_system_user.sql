
-- +goose Up
INSERT INTO users (id, issuer, sub, oidc_provider, display_name, source, provisioned_via, active,
                   created_by, updated_by, created_time, updated_time)
VALUES
    ('6f919ea1-0bcc-45d7-825a-f6fdbb388c1e', 'sam:system', 'system:evidence', '', 'Evidence', '{}', 'system', 1,
     'system', 'system', 1784678400000, 1784678400000)
ON CONFLICT (issuer, sub) DO NOTHING;

-- +goose Down

DELETE FROM users WHERE id = '6f919ea1-0bcc-45d7-825a-f6fdbb388c1e';
