-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION _to_uuid_sched(val TEXT) RETURNS UUID AS $$
DECLARE
    hex32 TEXT;
BEGIN
    IF val IS NULL THEN RETURN NULL; END IF;

    IF val ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN
        RETURN val::uuid;
    END IF;

    hex32 := regexp_replace(val, '^(scheduled_|sched-)', '');

    IF hex32 ~ '^[0-9a-fA-F]{32}$' THEN
        RETURN (
            substring(hex32, 1, 8) || '-' ||
            substring(hex32, 9, 4) || '-' ||
            substring(hex32, 13, 4) || '-' ||
            substring(hex32, 17, 4) || '-' ||
            substring(hex32, 21, 12)
        )::uuid;
    END IF;

    RETURN md5(val)::uuid;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

ALTER TABLE scheduled_task_executions DROP CONSTRAINT scheduled_task_executions_scheduled_task_id_fkey;

UPDATE scheduled_tasks SET id = _to_uuid_sched(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE scheduled_task_executions SET id = _to_uuid_sched(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE scheduled_task_executions SET scheduled_task_id = _to_uuid_sched(scheduled_task_id)::text
WHERE scheduled_task_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE scheduled_task_executions SET a2a_task_id = _to_uuid_sched(a2a_task_id)::text
WHERE a2a_task_id IS NOT NULL AND a2a_task_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

ALTER TABLE scheduled_tasks ALTER COLUMN id TYPE UUID USING id::uuid;

ALTER TABLE scheduled_task_executions ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE scheduled_task_executions ALTER COLUMN scheduled_task_id TYPE UUID USING scheduled_task_id::uuid;
ALTER TABLE scheduled_task_executions ALTER COLUMN a2a_task_id TYPE UUID USING a2a_task_id::uuid;

ALTER TABLE scheduled_task_executions ADD CONSTRAINT scheduled_task_executions_scheduled_task_id_fkey
    FOREIGN KEY (scheduled_task_id) REFERENCES scheduled_tasks(id) ON DELETE CASCADE;

DROP FUNCTION _to_uuid_sched(TEXT);

-- +goose Down

ALTER TABLE scheduled_task_executions DROP CONSTRAINT IF EXISTS scheduled_task_executions_scheduled_task_id_fkey;

ALTER TABLE scheduled_tasks ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE scheduled_task_executions ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE scheduled_task_executions ALTER COLUMN scheduled_task_id TYPE TEXT USING scheduled_task_id::text;
ALTER TABLE scheduled_task_executions ALTER COLUMN a2a_task_id TYPE TEXT USING a2a_task_id::text;

ALTER TABLE scheduled_task_executions ADD CONSTRAINT scheduled_task_executions_scheduled_task_id_fkey
    FOREIGN KEY (scheduled_task_id) REFERENCES scheduled_tasks(id) ON DELETE CASCADE;
