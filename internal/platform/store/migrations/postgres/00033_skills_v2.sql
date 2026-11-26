-- +goose Up

ALTER TABLE skills ADD COLUMN discovery_status TEXT NOT NULL DEFAULT 'pending';
UPDATE skills SET discovery_status = 'ready'  WHERE status = 'available';
UPDATE skills SET discovery_status = 'failed' WHERE status = 'unavailable';
ALTER TABLE skills DROP COLUMN status;

CREATE TABLE agent_skills (
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_id UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, skill_id)
);

CREATE INDEX idx_agent_skills_agent_id ON agent_skills(agent_id);
CREATE INDEX idx_agent_skills_skill_id ON agent_skills(skill_id);

-- +goose Down

DROP TABLE IF EXISTS agent_skills;

ALTER TABLE skills ADD COLUMN status TEXT NOT NULL DEFAULT 'available';
UPDATE skills SET status = 'available'   WHERE discovery_status = 'ready';
UPDATE skills SET status = 'unavailable' WHERE discovery_status = 'failed';
ALTER TABLE skills DROP COLUMN discovery_status;
