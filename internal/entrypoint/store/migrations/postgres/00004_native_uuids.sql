-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION _to_uuid(val TEXT) RETURNS UUID AS $$
DECLARE
    hex32 TEXT;
BEGIN
    IF val IS NULL THEN RETURN NULL; END IF;

    IF val ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN
        RETURN val::uuid;
    END IF;

    hex32 := regexp_replace(val, '^(web-session-|gdk-task-|a2a_subtask_)', '');

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

ALTER TABLE chat_tasks DROP CONSTRAINT chat_tasks_session_id_fkey;
ALTER TABLE task_events DROP CONSTRAINT task_events_task_id_fkey;
ALTER TABLE project_users DROP CONSTRAINT project_users_project_id_fkey;
ALTER TABLE prompts DROP CONSTRAINT prompts_group_id_fkey;
ALTER TABLE prompt_group_users DROP CONSTRAINT prompt_group_users_prompt_group_id_fkey;

UPDATE sessions SET id = _to_uuid(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE chat_tasks SET id = _to_uuid(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';
UPDATE chat_tasks SET session_id = _to_uuid(session_id)::text
WHERE session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE tasks SET id = _to_uuid(id)::text
WHERE id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';
UPDATE tasks SET parent_task_id = _to_uuid(parent_task_id)::text
WHERE parent_task_id IS NOT NULL AND parent_task_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';
UPDATE tasks SET session_id = _to_uuid(session_id)::text
WHERE session_id IS NOT NULL AND session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE task_events SET task_id = _to_uuid(task_id)::text
WHERE task_id IS NOT NULL AND task_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE sse_event_buffer SET task_id = _to_uuid(task_id)::text
WHERE task_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';
UPDATE sse_event_buffer SET session_id = _to_uuid(session_id)::text
WHERE session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE feedback SET task_id = _to_uuid(task_id)::text
WHERE task_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';
UPDATE feedback SET session_id = _to_uuid(session_id)::text
WHERE session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE shared_links SET session_id = _to_uuid(session_id)::text
WHERE session_id IS NOT NULL AND session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

ALTER TABLE sessions ALTER COLUMN id TYPE UUID USING id::uuid;

ALTER TABLE chat_tasks ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE chat_tasks ALTER COLUMN session_id TYPE UUID USING session_id::uuid;

ALTER TABLE tasks ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE tasks ALTER COLUMN parent_task_id TYPE UUID USING parent_task_id::uuid;
ALTER TABLE tasks ALTER COLUMN session_id TYPE UUID USING session_id::uuid;

ALTER TABLE task_events ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE task_events ALTER COLUMN task_id TYPE UUID USING task_id::uuid;

ALTER TABLE feedback ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE feedback ALTER COLUMN session_id TYPE UUID USING session_id::uuid;
ALTER TABLE feedback ALTER COLUMN task_id TYPE UUID USING task_id::uuid;

ALTER TABLE projects ALTER COLUMN id TYPE UUID USING id::uuid;

ALTER TABLE project_users ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE project_users ALTER COLUMN project_id TYPE UUID USING project_id::uuid;

ALTER TABLE prompt_groups ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE prompt_groups ALTER COLUMN production_prompt_id TYPE UUID USING production_prompt_id::uuid;

ALTER TABLE prompts ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE prompts ALTER COLUMN group_id TYPE UUID USING group_id::uuid;

ALTER TABLE prompt_group_users ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE prompt_group_users ALTER COLUMN prompt_group_id TYPE UUID USING prompt_group_id::uuid;

ALTER TABLE resource_access_shares ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE resource_access_shares ALTER COLUMN resource_id TYPE UUID USING resource_id::uuid;

ALTER TABLE shared_link_users ALTER COLUMN id TYPE UUID USING id::uuid;

ALTER TABLE sse_event_buffer ALTER COLUMN task_id TYPE UUID USING task_id::uuid;
ALTER TABLE sse_event_buffer ALTER COLUMN session_id TYPE UUID USING session_id::uuid;

ALTER TABLE shared_links ALTER COLUMN session_id TYPE UUID USING session_id::uuid;

ALTER TABLE sessions ALTER COLUMN project_id TYPE UUID USING project_id::uuid;

ALTER TABLE chat_tasks ADD CONSTRAINT chat_tasks_session_id_fkey
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;
ALTER TABLE task_events ADD CONSTRAINT task_events_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;
ALTER TABLE project_users ADD CONSTRAINT project_users_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE prompts ADD CONSTRAINT prompts_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES prompt_groups(id) ON DELETE CASCADE;
ALTER TABLE prompt_group_users ADD CONSTRAINT prompt_group_users_prompt_group_id_fkey
    FOREIGN KEY (prompt_group_id) REFERENCES prompt_groups(id) ON DELETE CASCADE;

DROP FUNCTION _to_uuid(TEXT);

-- +goose Down

ALTER TABLE chat_tasks DROP CONSTRAINT IF EXISTS chat_tasks_session_id_fkey;
ALTER TABLE task_events DROP CONSTRAINT IF EXISTS task_events_task_id_fkey;
ALTER TABLE project_users DROP CONSTRAINT IF EXISTS project_users_project_id_fkey;
ALTER TABLE prompts DROP CONSTRAINT IF EXISTS prompts_group_id_fkey;
ALTER TABLE prompt_group_users DROP CONSTRAINT IF EXISTS prompt_group_users_prompt_group_id_fkey;

ALTER TABLE sessions ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE chat_tasks ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE chat_tasks ALTER COLUMN session_id TYPE TEXT USING session_id::text;
ALTER TABLE tasks ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE tasks ALTER COLUMN parent_task_id TYPE TEXT USING parent_task_id::text;
ALTER TABLE tasks ALTER COLUMN session_id TYPE TEXT USING session_id::text;
ALTER TABLE task_events ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE task_events ALTER COLUMN task_id TYPE TEXT USING task_id::text;
ALTER TABLE feedback ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE feedback ALTER COLUMN session_id TYPE TEXT USING session_id::text;
ALTER TABLE feedback ALTER COLUMN task_id TYPE TEXT USING task_id::text;
ALTER TABLE projects ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE project_users ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE project_users ALTER COLUMN project_id TYPE TEXT USING project_id::text;
ALTER TABLE prompt_groups ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE prompt_groups ALTER COLUMN production_prompt_id TYPE TEXT USING production_prompt_id::text;
ALTER TABLE prompts ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE prompts ALTER COLUMN group_id TYPE TEXT USING group_id::text;
ALTER TABLE prompt_group_users ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE prompt_group_users ALTER COLUMN prompt_group_id TYPE TEXT USING prompt_group_id::text;
ALTER TABLE resource_access_shares ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE resource_access_shares ALTER COLUMN resource_id TYPE TEXT USING resource_id::text;
ALTER TABLE shared_link_users ALTER COLUMN id TYPE TEXT USING id::text;
ALTER TABLE sse_event_buffer ALTER COLUMN task_id TYPE TEXT USING task_id::text;
ALTER TABLE sse_event_buffer ALTER COLUMN session_id TYPE TEXT USING session_id::text;
ALTER TABLE shared_links ALTER COLUMN session_id TYPE TEXT USING session_id::text;
ALTER TABLE sessions ALTER COLUMN project_id TYPE TEXT USING project_id::text;

ALTER TABLE chat_tasks ADD CONSTRAINT chat_tasks_session_id_fkey
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE;
ALTER TABLE task_events ADD CONSTRAINT task_events_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;
ALTER TABLE project_users ADD CONSTRAINT project_users_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE prompts ADD CONSTRAINT prompts_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES prompt_groups(id) ON DELETE CASCADE;
ALTER TABLE prompt_group_users ADD CONSTRAINT prompt_group_users_prompt_group_id_fkey
    FOREIGN KEY (prompt_group_id) REFERENCES prompt_groups(id) ON DELETE CASCADE;
