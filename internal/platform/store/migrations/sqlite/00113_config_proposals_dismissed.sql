
-- +goose Up
UPDATE config_proposals SET status = 'dismissed' WHERE status = 'superseded' AND decided_by IS NOT NULL;
UPDATE config_proposals SET reject_reason = 'wrong_target' WHERE reject_reason = 'wrong_surface';

-- +goose Down
UPDATE config_proposals SET status = 'superseded' WHERE status = 'dismissed';
UPDATE config_proposals SET reject_reason = 'wrong_surface' WHERE reject_reason = 'wrong_target';
