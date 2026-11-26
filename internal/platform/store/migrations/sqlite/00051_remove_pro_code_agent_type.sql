-- +goose Up
DELETE FROM agents WHERE type = 'pro_code';

-- +goose Down
