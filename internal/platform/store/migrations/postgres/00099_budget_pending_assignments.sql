
-- +goose Up
ALTER TABLE budget_assignments ADD COLUMN IF NOT EXISTS identity_type TEXT NOT NULL DEFAULT '';
ALTER TABLE budget_assignments ADD COLUMN IF NOT EXISTS identity_value TEXT NOT NULL DEFAULT '';
ALTER TABLE budget_assignments ADD COLUMN IF NOT EXISTS issuer TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS budget_pending_assignments (
    id             UUID   PRIMARY KEY,
    subject_type   TEXT   NOT NULL,
    identity_type  TEXT   NOT NULL,
    identity_value TEXT   NOT NULL,
    issuer         TEXT   NOT NULL DEFAULT '',
    budget_id      UUID   NOT NULL,
    created_by     TEXT   NOT NULL,
    updated_by     TEXT   NOT NULL,
    created_time   BIGINT NOT NULL,
    updated_time   BIGINT NOT NULL,

    CONSTRAINT fk_budget_pending_assignments_budget
        FOREIGN KEY (budget_id, subject_type)
        REFERENCES budgets(id, subject_type) ON DELETE RESTRICT,
    CONSTRAINT ck_budget_pending_assignments_subject_type
        CHECK (subject_type = 'user'),
    CONSTRAINT ck_budget_pending_assignments_identity_type
        CHECK (identity_type IN ('email', 'sub')),
    CONSTRAINT ck_budget_pending_assignments_value_present
        CHECK (length(identity_value) > 0),
    CONSTRAINT ck_budget_pending_assignments_sub_issuer
        CHECK (identity_type <> 'sub' OR issuer <> ''),
    CONSTRAINT uq_budget_pending_assignments_identity
        UNIQUE (subject_type, identity_type, identity_value, issuer)
);

CREATE INDEX IF NOT EXISTS ix_budget_pending_assignments_budget
    ON budget_pending_assignments(budget_id);

-- +goose Down
DROP INDEX IF EXISTS ix_budget_pending_assignments_budget;
DROP TABLE IF EXISTS budget_pending_assignments;
ALTER TABLE budget_assignments DROP COLUMN IF EXISTS issuer;
ALTER TABLE budget_assignments DROP COLUMN IF EXISTS identity_value;
ALTER TABLE budget_assignments DROP COLUMN IF EXISTS identity_type;
