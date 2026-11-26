-- +goose Up

DELETE FROM skills WHERE origin = 'filesystem';

-- +goose Down
