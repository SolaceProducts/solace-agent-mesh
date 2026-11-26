
-- +goose Up
CREATE TABLE IF NOT EXISTS config_proposal_changes (
    proposal_id        TEXT NOT NULL REFERENCES config_proposals(id) ON DELETE CASCADE,
    ordinal            INTEGER NOT NULL CHECK (ordinal >= 0),
    surface            TEXT NOT NULL,
    target_ref         TEXT NOT NULL,
    target_name        TEXT NOT NULL,
    base_version_hash  TEXT NOT NULL,
    current_value      TEXT NOT NULL,
    proposed_value     TEXT NOT NULL,
    reason             TEXT NOT NULL,
    is_open            BOOLEAN NOT NULL,
    PRIMARY KEY (proposal_id, ordinal)
);

INSERT INTO config_proposal_changes (proposal_id, ordinal, surface, target_ref, target_name, base_version_hash,
    current_value, proposed_value, reason, is_open)
SELECT id, 0, surface, target_ref, target_name, base_version_hash, current_value, proposed_value, '',
    status IN ('proposed', 'approved')
FROM config_proposals WHERE status <> 'discarded'
ON CONFLICT DO NOTHING;

CREATE UNIQUE INDEX IF NOT EXISTS ux_config_proposal_changes_open_target
    ON config_proposal_changes(surface, target_ref) WHERE is_open;

CREATE INDEX IF NOT EXISTS ix_config_proposal_changes_target ON config_proposal_changes(surface, target_ref);

-- +goose Down
DROP INDEX IF EXISTS ix_config_proposal_changes_target;
DROP INDEX IF EXISTS ux_config_proposal_changes_open_target;
DROP TABLE IF EXISTS config_proposal_changes;
