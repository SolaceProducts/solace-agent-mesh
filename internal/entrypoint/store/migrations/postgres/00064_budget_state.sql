-- +goose Up

CREATE TABLE IF NOT EXISTS gateway_budget_state (
    id           INTEGER PRIMARY KEY,
    state        TEXT    NOT NULL,
    updated_time BIGINT  NOT NULL,
    CONSTRAINT ck_gateway_budget_state_one_row CHECK (id = 1)
);

-- +goose Down
DROP TABLE IF EXISTS gateway_budget_state;
