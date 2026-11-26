
-- +goose Up
CREATE TABLE IF NOT EXISTS budgets (
    id              TEXT    PRIMARY KEY,
    name            TEXT    NOT NULL,
    name_lower      TEXT    NOT NULL,
    subject_type    TEXT    NOT NULL,
    amount          BIGINT,
    warn_percentage INTEGER,
    is_default      INTEGER NOT NULL DEFAULT 0,
    created_by      TEXT    NOT NULL,
    updated_by      TEXT    NOT NULL,
    created_time    BIGINT  NOT NULL,
    updated_time    BIGINT  NOT NULL,

    CONSTRAINT ck_budgets_amount_nonneg
        CHECK (amount IS NULL OR amount >= 0),
    CONSTRAINT ck_budgets_warn_requires_amount
        CHECK (amount IS NOT NULL OR warn_percentage IS NULL),
    CONSTRAINT ck_budgets_name_length
        CHECK (length(name) >= 3 AND length(name) <= 255),
    CONSTRAINT ck_budgets_warn_range
        CHECK (warn_percentage IS NULL
               OR (warn_percentage > 0 AND warn_percentage <= 100)),
    CONSTRAINT ck_budgets_is_default_binary
        CHECK (is_default IN (0, 1))
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_budgets_name ON budgets(name_lower);

CREATE UNIQUE INDEX IF NOT EXISTS ux_budgets_default
    ON budgets(subject_type, is_default) WHERE is_default = 1;

CREATE UNIQUE INDEX IF NOT EXISTS ux_budgets_mesh_singleton
    ON budgets(subject_type) WHERE subject_type = 'mesh';

INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, is_default,
                     created_by, updated_by, created_time, updated_time)
VALUES
    ('00000000-0000-0000-0000-00000000b001', 'Agent Mesh', 'agent mesh', 'mesh', NULL, NULL, 1,
     'system', 'system', CAST(strftime('%s', 'now') AS INTEGER) * 1000, CAST(strftime('%s', 'now') AS INTEGER) * 1000),
    ('00000000-0000-0000-0000-00000000b002', 'Default User', 'default user', 'user', NULL, NULL, 1,
     'system', 'system', CAST(strftime('%s', 'now') AS INTEGER) * 1000, CAST(strftime('%s', 'now') AS INTEGER) * 1000);

-- +goose Down
DELETE FROM budgets
 WHERE id IN ('00000000-0000-0000-0000-00000000b001', '00000000-0000-0000-0000-00000000b002');
DROP INDEX IF EXISTS ux_budgets_mesh_singleton;
DROP INDEX IF EXISTS ux_budgets_default;
DROP INDEX IF EXISTS ux_budgets_name;
DROP TABLE IF EXISTS budgets;
