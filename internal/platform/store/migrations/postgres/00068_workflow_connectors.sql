
-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_connectors (
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    connector_id UUID NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    ref_name TEXT NOT NULL,
    PRIMARY KEY (workflow_id, connector_id),
    UNIQUE (workflow_id, ref_name)
);
CREATE INDEX IF NOT EXISTS idx_workflow_connectors_connector ON workflow_connectors(connector_id);

-- +goose Down
DROP INDEX IF EXISTS idx_workflow_connectors_connector;
DROP TABLE IF EXISTS workflow_connectors;
