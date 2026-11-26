-- +goose Up

WITH first_req AS (
    SELECT
        e.task_id,
        substr(e.topic, instr(e.topic, '/agent/request/') + 15) AS rest,
        ROW_NUMBER() OVER (
            PARTITION BY e.task_id ORDER BY e.created_time ASC, e.id ASC
        ) AS rn
    FROM task_events e
    WHERE e.direction = 'request'
      AND e.topic LIKE '%/agent/request/%'
)
UPDATE tasks
SET handler_name = (
    SELECT
        CASE
            WHEN fr.rest = '' THEN NULL
            WHEN instr(fr.rest, '/') > 0 THEN substr(fr.rest, 1, instr(fr.rest, '/') - 1)
            ELSE fr.rest
        END
    FROM first_req fr
    WHERE fr.task_id = tasks.id AND fr.rn = 1
)
WHERE handler_name IS NULL
  AND EXISTS (
    SELECT 1 FROM first_req fr2 WHERE fr2.task_id = tasks.id AND fr2.rn = 1
  );

UPDATE tasks SET handler_type = 'workflow'
WHERE handler_type IS NULL
  AND handler_name LIKE 'workflow\_%' ESCAPE '\';

UPDATE tasks SET handler_type = 'agent'
WHERE handler_type IS NULL
  AND handler_name LIKE 'agent\_%' ESCAPE '\';

UPDATE tasks SET handler_type = 'workflow'
WHERE handler_type IS NULL
  AND handler_name IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM task_events te
    WHERE te.task_id = tasks.id
      AND te.payload LIKE '%workflow_execution_start%'
  );

UPDATE tasks SET handler_type = 'agent'
WHERE handler_type IS NULL
  AND handler_name IS NOT NULL;

-- +goose Down
