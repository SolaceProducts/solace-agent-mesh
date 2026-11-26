
-- +goose Up
CREATE TEMPORARY TABLE toolset_alias_map (alias TEXT PRIMARY KEY, canonical TEXT NOT NULL);
INSERT INTO toolset_alias_map (alias, canonical) VALUES
    ('artifact_management', 'builtin_artifact_tools'),
    ('web_tools', 'builtin_web_request_tools'),
    ('research', 'builtin_research_tools'),
    ('image_tools', 'builtin_image_tools'),
    ('code_execution', 'builtin_code_execution');

DELETE FROM agent_toolsets
WHERE (agent_id, toolset) IN (
    SELECT alias_row.agent_id, alias_row.toolset
    FROM agent_toolsets alias_row
    JOIN toolset_alias_map m ON m.alias = alias_row.toolset
    JOIN agent_toolsets canonical_row
      ON canonical_row.agent_id = alias_row.agent_id
     AND canonical_row.toolset = m.canonical
);

UPDATE agent_toolsets
SET toolset = (SELECT m.canonical FROM toolset_alias_map m WHERE m.alias = agent_toolsets.toolset)
WHERE toolset IN (SELECT alias FROM toolset_alias_map);

DELETE FROM agent_builtin_toolset_configs
WHERE (agent_id, toolset_name) IN (
    SELECT alias_row.agent_id, alias_row.toolset_name
    FROM agent_builtin_toolset_configs alias_row
    JOIN toolset_alias_map m ON m.alias = alias_row.toolset_name
    JOIN agent_builtin_toolset_configs canonical_row
      ON canonical_row.agent_id = alias_row.agent_id
     AND canonical_row.toolset_name = m.canonical
);

UPDATE agent_builtin_toolset_configs
SET toolset_name = (SELECT m.canonical FROM toolset_alias_map m WHERE m.alias = agent_builtin_toolset_configs.toolset_name)
WHERE toolset_name IN (SELECT alias FROM toolset_alias_map);

DELETE FROM workflow_toolsets
WHERE (workflow_id, toolset) IN (
    SELECT alias_row.workflow_id, alias_row.toolset
    FROM workflow_toolsets alias_row
    JOIN toolset_alias_map m ON m.alias = alias_row.toolset
    JOIN workflow_toolsets canonical_row
      ON canonical_row.workflow_id = alias_row.workflow_id
     AND canonical_row.toolset = m.canonical
);

UPDATE workflow_toolsets
SET toolset = (SELECT m.canonical FROM toolset_alias_map m WHERE m.alias = workflow_toolsets.toolset)
WHERE toolset IN (SELECT alias FROM toolset_alias_map);

DROP TABLE toolset_alias_map;

-- +goose Down
SELECT 1;
