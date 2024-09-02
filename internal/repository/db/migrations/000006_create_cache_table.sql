-- +goose Up
CREATE TABLE IF NOT EXISTS cache(
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL,
    expires_at
);

CREATE INDEX IF NOT EXISTS cache_expires_at_index ON cache(expires_at);

-- +goose Down
DROP TABLE IF EXISTS cache;
DROP INDEX IF EXISTS cache_expires_at_index;