
-- +goose Up
CREATE TABLE IF NOT EXISTS budget_top_ups (
    id           UUID   PRIMARY KEY,
    subject_type TEXT   NOT NULL,
    subject_ref  TEXT   NOT NULL,
    amount       BIGINT NOT NULL,
    expires_time BIGINT NOT NULL,
    created_by   TEXT   NOT NULL,
    updated_by   TEXT   NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,

    CONSTRAINT ck_budget_top_ups_subject_type
        CHECK (subject_type IN ('mesh', 'user')),
    CONSTRAINT ck_budget_top_ups_mesh_ref
        CHECK (subject_type <> 'mesh' OR subject_ref = 'mesh'),
    CONSTRAINT ck_budget_top_ups_ref_present
        CHECK (length(subject_ref) > 0),
    CONSTRAINT ck_budget_top_ups_amount_positive
        CHECK (amount > 0),
    CONSTRAINT ck_budget_top_ups_expires_after_created
        CHECK (expires_time > created_time),
    CONSTRAINT uq_budget_top_ups_subject_expiry
        UNIQUE (subject_type, subject_ref, expires_time)
);

-- +goose Down
DROP TABLE IF EXISTS budget_top_ups;
