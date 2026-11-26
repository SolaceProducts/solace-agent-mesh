-- +goose Up
UPDATE agent_deployments
   SET status        = 'failed',
       error_message = COALESCE(error_message, 'finalized by migration: pending deployment from previous schema'),
       updated_time  = (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint
 WHERE status = 'pending';

UPDATE workflow_deployments
   SET status        = 'failed',
       error_message = COALESCE(error_message, 'finalized by migration: pending deployment from previous schema'),
       updated_time  = (EXTRACT(EPOCH FROM NOW()) * 1000)::bigint
 WHERE status = 'pending';

-- +goose Down
SELECT 1;
