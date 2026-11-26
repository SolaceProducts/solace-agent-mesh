
-- +goose Up
ALTER TABLE config_proposals DROP CONSTRAINT IF EXISTS config_proposals_status_check;
ALTER TABLE config_proposals DROP CONSTRAINT IF EXISTS config_proposals_reject_reason_check;

UPDATE config_proposals SET status = 'dismissed' WHERE status = 'superseded' AND decided_by IS NOT NULL;
UPDATE config_proposals SET reject_reason = 'wrong_target' WHERE reject_reason = 'wrong_surface';

ALTER TABLE config_proposals ADD CONSTRAINT config_proposals_status_check CHECK (status IN
    ('proposed', 'approved', 'merged', 'closed', 'rejected', 'superseded', 'dismissed', 'discarded'));
ALTER TABLE config_proposals ADD CONSTRAINT config_proposals_reject_reason_check CHECK (reject_reason IS NULL OR reject_reason IN
    ('wrong_diagnosis', 'bad_execution', 'wrong_target', 'wrong_surface', 'not_now'));

-- +goose Down
ALTER TABLE config_proposals DROP CONSTRAINT IF EXISTS config_proposals_status_check;
ALTER TABLE config_proposals DROP CONSTRAINT IF EXISTS config_proposals_reject_reason_check;

UPDATE config_proposals SET status = 'superseded' WHERE status = 'dismissed';
UPDATE config_proposals SET reject_reason = 'wrong_surface' WHERE reject_reason = 'wrong_target';

ALTER TABLE config_proposals ADD CONSTRAINT config_proposals_status_check CHECK (status IN
    ('proposed', 'approved', 'merged', 'closed', 'rejected', 'superseded', 'discarded'));
ALTER TABLE config_proposals ADD CONSTRAINT config_proposals_reject_reason_check CHECK (reject_reason IS NULL OR reject_reason IN
    ('wrong_diagnosis', 'bad_execution', 'wrong_surface', 'not_now'));
