
-- +goose Up
INSERT INTO users (id, issuer, sub, oidc_provider, display_name, source, provisioned_via, active,
                   created_by, updated_by, created_time, updated_time)
VALUES
    ('6dbe83db-3549-4d99-b4fb-c3aea7b75219', 'sam:system', 'system:default', '', 'Default', '{}', 'system', 1,
     'system', 'system', 1784678400000, 1784678400000),
    ('1f8ffb5d-fc1b-47b3-8578-559670c859ae', 'sam:system', 'system:channel', '', 'Channel', '{}', 'system', 1,
     'system', 'system', 1784678400000, 1784678400000),
    ('a38ff022-96f3-4a78-a2e5-8a71bfa882ab', 'sam:system', 'system:eval', '', 'Eval', '{}', 'system', 1,
     'system', 'system', 1784678400000, 1784678400000)
ON CONFLICT (issuer, sub) DO NOTHING;

-- +goose Down

DELETE FROM users WHERE id IN (
    '6dbe83db-3549-4d99-b4fb-c3aea7b75219',
    '1f8ffb5d-fc1b-47b3-8578-559670c859ae',
    'a38ff022-96f3-4a78-a2e5-8a71bfa882ab'
);
