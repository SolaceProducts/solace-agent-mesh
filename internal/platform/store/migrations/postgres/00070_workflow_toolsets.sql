
-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_toolsets (
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    toolset TEXT NOT NULL,
    PRIMARY KEY (workflow_id, toolset)
);
CREATE INDEX IF NOT EXISTS idx_workflow_toolsets_toolset ON workflow_toolsets(toolset);

-- +goose Down
DROP INDEX IF EXISTS idx_workflow_toolsets_toolset;
DROP TABLE IF EXISTS workflow_toolsets;
