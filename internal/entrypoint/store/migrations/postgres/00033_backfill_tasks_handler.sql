-- +goose NO TRANSACTION
-- +goose Up

-- +goose StatementBegin
DO $$
DECLARE
  batch_size CONSTANT int := 5000;
  cursor_min uuid := '00000000-0000-0000-0000-000000000000'::uuid;
  batch_max uuid;
BEGIN
  LOOP
    -- Fetch the batch's max ID by scanning the PK index. Grabbed as the last
    -- row of the ASC-ordered slice (ORDER BY id DESC LIMIT 1 on the subquery)
    -- rather than MAX(id) — Postgres builds without a MAX(uuid) aggregate
    -- reject the aggregate call.
    SELECT id INTO batch_max FROM (
      SELECT id FROM tasks
      WHERE id > cursor_min
      ORDER BY id
      LIMIT batch_size
    ) sub
    ORDER BY id DESC
    LIMIT 1;

    EXIT WHEN NOT FOUND;

    -- Step 1: handler_name from the earliest /agent/request/ event topic.
    -- The task_events join is restricted to this batch's ID range so it
    -- uses idx_task_events_task_id and stays bounded per iteration.
    WITH first_req AS (
      SELECT DISTINCT ON (e.task_id)
        e.task_id,
        substring(e.topic FROM '/agent/request/([^/]+)') AS handler_name
      FROM task_events e
      WHERE e.task_id > cursor_min
        AND e.task_id <= batch_max
        AND e.direction = 'request'
        AND e.topic LIKE '%/agent/request/%'
      ORDER BY e.task_id, e.created_time ASC, e.id ASC
    )
    UPDATE tasks t
    SET handler_name = fr.handler_name
    FROM first_req fr
    WHERE t.id = fr.task_id
      AND t.handler_name IS NULL;

    -- Step 2: platform-deployed components carry a fixed "agent_<uuid>" /
    -- "workflow_<uuid>" broker name for their whole lifetime (a2a.InstanceName)
    -- — decisive on its own, no event content involved. Classify these first
    -- so the payload-scan fallback below (Step 3) never gets a chance to
    -- misclassify them: it can go either way — a workflow that crashed before
    -- emitting workflow_execution_start would otherwise backfill as 'agent',
    -- and an agent that merely delegated to a workflow can have that
    -- workflow's start signal echoed into its own task_events (PeerRouter
    -- status forwarding), which would otherwise backfill it as 'workflow'.
    UPDATE tasks t
    SET handler_type = 'workflow'
    WHERE t.id > cursor_min
      AND t.id <= batch_max
      AND t.handler_type IS NULL
      AND t.handler_name LIKE 'workflow\_%' ESCAPE '\';

    UPDATE tasks t
    SET handler_type = 'agent'
    WHERE t.id > cursor_min
      AND t.id <= batch_max
      AND t.handler_type IS NULL
      AND t.handler_name LIKE 'agent\_%' ESCAPE '\';

    -- Step 3: fallback for plain-named handlers (no platform-assigned
    -- prefix) — mark tasks that carry a workflow_execution_start signal in
    -- any of their events. The payload LIKE is expensive but bounded to
    -- the batch's tasks (uses idx_task_events_task_id per task). Step 2's
    -- IS NULL gate means this is only ever reached by rows Step 2 didn't
    -- already resolve.
    UPDATE tasks t
    SET handler_type = 'workflow'
    WHERE t.id > cursor_min
      AND t.id <= batch_max
      AND t.handler_type IS NULL
      AND t.handler_name IS NOT NULL
      AND EXISTS (
        SELECT 1 FROM task_events te
        WHERE te.task_id = t.id
          AND te.payload LIKE '%workflow_execution_start%'
      );

    -- Step 4: remaining resolved tasks are agent invocations. No
    -- parent-propagation: an agent invocation dispatched by a workflow
    -- still classifies as 'agent' by its own topic.
    UPDATE tasks t
    SET handler_type = 'agent'
    WHERE t.id > cursor_min
      AND t.id <= batch_max
      AND t.handler_type IS NULL
      AND t.handler_name IS NOT NULL;

    cursor_min := batch_max;
    COMMIT;
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
