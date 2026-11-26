-- +goose Up
UPDATE agents SET model_provider = 'general' WHERE model_provider IS NULL AND type = 'standard';

-- +goose Down
UPDATE agents SET model_provider = NULL WHERE model_provider = 'general' AND type = 'standard';
