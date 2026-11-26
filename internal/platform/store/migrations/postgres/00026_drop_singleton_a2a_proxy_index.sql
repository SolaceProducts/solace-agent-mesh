-- +goose Up
DROP INDEX IF EXISTS idx_agents_single_a2a_proxy;

-- +goose Down
CREATE UNIQUE INDEX idx_agents_single_a2a_proxy ON agents ((1)) WHERE type = 'a2a_proxy';
