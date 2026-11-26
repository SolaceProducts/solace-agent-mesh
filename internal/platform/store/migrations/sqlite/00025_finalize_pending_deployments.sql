-- +goose Up
UPDATE agent_deployments
   SET status        = 'failed',
       error_message = COALESCE(error_message, 'finalized by migration: pending deployment from previous schema'),
       updated_time  = (CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER))
 WHERE status = 'pending';

UPDATE workflow_deployments
   SET status        = 'failed',
       error_message = COALESCE(error_message, 'finalized by migration: pending deployment from previous schema'),
       updated_time  = (CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER))
 WHERE status = 'pending';

-- +goose Down
SELECT 1;
