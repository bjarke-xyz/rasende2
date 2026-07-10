-- +goose Up
DROP INDEX IF EXISTS cache_expires_at_index;
DROP TABLE IF EXISTS cache;

-- +goose Down
CREATE TABLE IF NOT EXISTS cache(
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL,
    expires_at
);

CREATE INDEX IF NOT EXISTS cache_expires_at_index ON cache(expires_at);
