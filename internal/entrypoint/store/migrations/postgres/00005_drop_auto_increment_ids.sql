-- +goose Up

ALTER TABLE sse_event_buffer DROP CONSTRAINT sse_event_buffer_pkey;
ALTER TABLE sse_event_buffer DROP COLUMN id;
ALTER TABLE sse_event_buffer DROP CONSTRAINT sse_event_buffer_task_id_event_sequence_key;
ALTER TABLE sse_event_buffer ADD PRIMARY KEY (task_id, event_sequence);

ALTER TABLE shared_artifacts DROP CONSTRAINT shared_artifacts_pkey;
ALTER TABLE shared_artifacts DROP COLUMN id;
ALTER TABLE shared_artifacts ADD PRIMARY KEY (share_id, artifact_uri);

-- +goose Down

ALTER TABLE sse_event_buffer DROP CONSTRAINT sse_event_buffer_pkey;
ALTER TABLE sse_event_buffer ADD COLUMN id SERIAL;
ALTER TABLE sse_event_buffer ADD PRIMARY KEY (id);
ALTER TABLE sse_event_buffer ADD CONSTRAINT sse_event_buffer_task_id_event_sequence_key
    UNIQUE (task_id, event_sequence);

ALTER TABLE shared_artifacts DROP CONSTRAINT shared_artifacts_pkey;
ALTER TABLE shared_artifacts ADD COLUMN id SERIAL;
ALTER TABLE shared_artifacts ADD PRIMARY KEY (id);
