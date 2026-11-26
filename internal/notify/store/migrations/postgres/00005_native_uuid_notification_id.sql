-- +goose Up

ALTER TABLE notifications ALTER COLUMN id TYPE uuid USING id::uuid;

-- +goose Down

ALTER TABLE notifications ALTER COLUMN id TYPE text USING id::text;
