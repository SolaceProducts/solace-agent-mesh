
-- +goose Up
CREATE TABLE system_seed_state (
    seed_key TEXT PRIMARY KEY,
    seeded_at BIGINT NOT NULL
);

-- +goose Down
DROP TABLE system_seed_state;
