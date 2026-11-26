-- +goose Up

UPDATE shared_link_users SET access_level = LOWER(access_level)
    WHERE access_level <> LOWER(access_level);
UPDATE shared_link_users SET original_access_level = LOWER(original_access_level)
    WHERE original_access_level IS NOT NULL
      AND original_access_level <> LOWER(original_access_level);
UPDATE resource_access_shares SET access_level = LOWER(access_level)
    WHERE access_level <> LOWER(access_level);

ALTER TABLE shared_link_users ALTER COLUMN access_level SET DEFAULT 'resource_viewer';

-- +goose Down

ALTER TABLE shared_link_users ALTER COLUMN access_level SET DEFAULT 'RESOURCE_VIEWER';

UPDATE shared_link_users SET access_level = UPPER(access_level)
    WHERE access_level <> UPPER(access_level);
UPDATE shared_link_users SET original_access_level = UPPER(original_access_level)
    WHERE original_access_level IS NOT NULL
      AND original_access_level <> UPPER(original_access_level);
UPDATE resource_access_shares SET access_level = UPPER(access_level)
    WHERE access_level <> UPPER(access_level);
