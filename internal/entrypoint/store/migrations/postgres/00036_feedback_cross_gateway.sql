-- +goose Up

LOCK TABLE feedback IN ACCESS EXCLUSIVE MODE;
CREATE TABLE feedback_new (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    session_id UUID NOT NULL,
    rating TEXT NOT NULL,
    comment TEXT,
    share_content INTEGER NOT NULL DEFAULT 0,
    subject_id TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    entrypoint_type TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE (task_id, user_id)
);
INSERT INTO feedback_new (id, task_id, user_id, session_id, rating, comment, share_content,
                          subject_id, subject_type, entrypoint_type, created_at, updated_at)
SELECT id, task_id, user_id, session_id, rating, comment, share_content,
       CASE
           WHEN wf_is_uuid = 1 THEN wf_body
           WHEN ag_is_uuid = 1 THEN ag_body
           WHEN agent_name IS NOT NULL AND agent_name <> '' THEN agent_name
           ELSE unk
       END,
       CASE
           WHEN wf_is_uuid = 1 THEN 'workflow'
           WHEN ag_is_uuid = 1 THEN 'agent'
           WHEN agent_name IS NOT NULL AND agent_name <> '' THEN 'agent'
           ELSE unk
       END,
       unk, created_time, created_time
FROM (
    SELECT bodies.*,
           CASE WHEN wf_body ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN 1 ELSE 0 END AS wf_is_uuid,
           CASE WHEN ag_body ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN 1 ELSE 0 END AS ag_is_uuid
    FROM (
        SELECT f.id, f.task_id, f.user_id, f.session_id, f.rating, f.comment, f.share_content,
               f.created_time, t.agent_name, 'unknown' AS unk,
               CASE WHEN substr(t.agent_name, 1, 9) = 'workflow_' THEN replace(substr(t.agent_name, 10), '_', '-') ELSE '' END AS wf_body,
               CASE WHEN substr(t.agent_name, 1, 6) = 'agent_' THEN replace(substr(t.agent_name, 7), '_', '-') ELSE '' END AS ag_body,
               ROW_NUMBER() OVER (PARTITION BY f.task_id, f.user_id
                                  ORDER BY f.created_time DESC, f.id DESC) AS rn
        FROM feedback f
        LEFT JOIN tasks t ON t.id = f.task_id
    ) AS bodies
) AS ranked
WHERE rn = 1;
DROP TABLE feedback;
ALTER TABLE feedback_new RENAME TO feedback;
ALTER TABLE feedback RENAME CONSTRAINT feedback_new_pkey TO feedback_pkey;
ALTER TABLE feedback RENAME CONSTRAINT feedback_new_task_id_user_id_key TO feedback_task_id_user_id_key;
CREATE INDEX IF NOT EXISTS idx_feedback_subject_updated ON feedback(subject_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_feedback_share_content ON feedback(task_id, share_content);
CREATE INDEX IF NOT EXISTS idx_feedback_updated_at ON feedback(updated_at);
CREATE INDEX IF NOT EXISTS idx_feedback_user_id ON feedback(user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_session ON feedback(session_id);

-- +goose Down

LOCK TABLE feedback IN ACCESS EXCLUSIVE MODE;
CREATE TABLE feedback_old (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    task_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    rating TEXT NOT NULL,
    comment TEXT,
    created_time BIGINT NOT NULL,
    share_content INTEGER NOT NULL DEFAULT 0
);
INSERT INTO feedback_old (id, session_id, task_id, user_id, rating, comment, created_time, share_content)
SELECT id, session_id, task_id, user_id, rating, comment, updated_at, share_content
FROM feedback;
DROP TABLE feedback;
ALTER TABLE feedback_old RENAME TO feedback;
ALTER TABLE feedback RENAME CONSTRAINT feedback_old_pkey TO feedback_pkey;
CREATE INDEX IF NOT EXISTS idx_feedback_task_id ON feedback(task_id);
CREATE INDEX IF NOT EXISTS idx_feedback_user_id ON feedback(user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_created_time ON feedback(created_time);
CREATE INDEX IF NOT EXISTS idx_feedback_session ON feedback(session_id);
CREATE INDEX IF NOT EXISTS idx_feedback_share_content ON feedback(task_id, share_content);
