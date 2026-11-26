
-- +goose Up
CREATE TABLE IF NOT EXISTS config_proposals (
    id                 UUID PRIMARY KEY,
    surface            TEXT NOT NULL,
    handler_type       TEXT,
    handler_name       TEXT NOT NULL,
    target_ref         TEXT NOT NULL,
    target_name        TEXT NOT NULL,
    finding_id         TEXT NOT NULL,
    analyzer_version   TEXT,
    base_version_hash  TEXT NOT NULL,
    current_value      TEXT NOT NULL,
    proposed_value     TEXT NOT NULL,
    summary            TEXT NOT NULL,
    rationale          TEXT NOT NULL,
    evidence           TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN
        ('proposed', 'approved', 'merged', 'closed', 'rejected', 'superseded', 'discarded')),
    reject_reason      TEXT CHECK (reject_reason IS NULL OR reject_reason IN
        ('wrong_diagnosis', 'bad_execution', 'wrong_surface', 'not_now')),
    discard_reason     TEXT,
    decided_by         TEXT,
    decided_time       BIGINT,
    pr_branch          TEXT,
    pr_url             TEXT,
    pr_number          INTEGER,
    pr_base_sha        TEXT,
    model              TEXT NOT NULL,
    created_time       BIGINT NOT NULL,
    updated_time       BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_config_proposals_open_target ON config_proposals(surface, target_ref)
    WHERE status IN ('proposed', 'approved');

CREATE INDEX IF NOT EXISTS ix_config_proposals_status ON config_proposals(status, created_time);

CREATE INDEX IF NOT EXISTS ix_config_proposals_finding ON config_proposals(finding_id, decided_time);

-- +goose Down
DROP INDEX IF EXISTS ix_config_proposals_finding;
DROP INDEX IF EXISTS ix_config_proposals_status;
DROP INDEX IF EXISTS ux_config_proposals_open_target;
DROP TABLE IF EXISTS config_proposals;
