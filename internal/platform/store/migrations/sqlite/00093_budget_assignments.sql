
-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS ux_budgets_id_subject_type
    ON budgets(id, subject_type);

CREATE TABLE IF NOT EXISTS budget_assignments (
    id           TEXT   PRIMARY KEY,
    subject_type TEXT   NOT NULL,
    subject_ref  TEXT   NOT NULL,
    budget_id    TEXT   NOT NULL,
    created_by   TEXT   NOT NULL,
    updated_by   TEXT   NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,

    CONSTRAINT fk_budget_assignments_budget
        FOREIGN KEY (budget_id, subject_type)
        REFERENCES budgets(id, subject_type) ON DELETE RESTRICT,
    CONSTRAINT ck_budget_assignments_ref_present
        CHECK (length(subject_ref) > 0),
    CONSTRAINT uq_budget_assignments_subject
        UNIQUE (subject_type, subject_ref)
);

CREATE INDEX IF NOT EXISTS ix_budget_assignments_budget
    ON budget_assignments(budget_id);

-- +goose Down
DROP INDEX IF EXISTS ix_budget_assignments_budget;
DROP TABLE IF EXISTS budget_assignments;
DROP INDEX IF EXISTS ux_budgets_id_subject_type;
