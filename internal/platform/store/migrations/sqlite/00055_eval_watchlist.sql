
-- +goose Up
CREATE TABLE eval_agent_watchlist (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    created_time INTEGER NOT NULL,
    UNIQUE (user_id, agent_name)
);

CREATE INDEX ix_eval_watchlist_user ON eval_agent_watchlist(user_id);

-- +goose Down
DROP INDEX ix_eval_watchlist_user;
DROP TABLE eval_agent_watchlist;
