
-- +goose Up
CREATE TABLE eval_example_artifacts (
    id TEXT PRIMARY KEY,
    example_id TEXT NOT NULL REFERENCES eval_examples(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('input', 'expected')),
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    version INTEGER NOT NULL,
    artifact_session_id TEXT NOT NULL,
    content_hash TEXT NOT NULL CHECK (length(content_hash) > 0),
    created_time INTEGER NOT NULL,
    updated_time INTEGER NOT NULL,
    UNIQUE (example_id, role)
);

ALTER TABLE eval_run_results ADD COLUMN input_artifact_metadata TEXT;
ALTER TABLE eval_run_results ADD COLUMN expected_artifact_metadata TEXT;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN expected_artifact_metadata;
ALTER TABLE eval_run_results DROP COLUMN input_artifact_metadata;

DROP TABLE eval_example_artifacts;
