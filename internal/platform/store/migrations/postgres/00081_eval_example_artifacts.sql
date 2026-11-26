
-- +goose Up
CREATE TABLE eval_example_artifacts (
    id UUID PRIMARY KEY,
    example_id UUID NOT NULL REFERENCES eval_examples(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('input', 'expected')),
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    version INTEGER NOT NULL,
    artifact_session_id TEXT NOT NULL,
    content_hash TEXT NOT NULL CHECK (length(content_hash) > 0),
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,
    CONSTRAINT uq_eval_example_artifacts_example_role UNIQUE (example_id, role)
);

ALTER TABLE eval_run_results ADD COLUMN input_artifact_metadata JSONB;
ALTER TABLE eval_run_results ADD COLUMN expected_artifact_metadata JSONB;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN expected_artifact_metadata;
ALTER TABLE eval_run_results DROP COLUMN input_artifact_metadata;

DROP TABLE eval_example_artifacts;
