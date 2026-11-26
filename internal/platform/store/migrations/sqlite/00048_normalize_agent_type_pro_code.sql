-- +goose Up

UPDATE agents SET type = 'pro_code' WHERE type = 'pro-code';

-- +goose Down
UPDATE agents SET type = 'pro-code' WHERE type = 'pro_code';
