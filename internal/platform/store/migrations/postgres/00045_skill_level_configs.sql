
-- +goose Up
CREATE TABLE skill_configs (
    skill_id      UUID PRIMARY KEY REFERENCES skills(id) ON DELETE CASCADE,
    config_values TEXT,
    updated_by    TEXT NOT NULL DEFAULT '',
    updated_time  BIGINT NOT NULL
);

ALTER TABLE agent_skills ADD COLUMN config_values TEXT;

-- +goose Down
DROP TABLE IF EXISTS skill_configs;
ALTER TABLE agent_skills DROP COLUMN IF EXISTS config_values;
