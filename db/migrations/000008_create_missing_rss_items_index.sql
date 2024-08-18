-- +goose Up
CREATE INDEX IF NOT EXISTS site_inserted_index ON rss_items(site_id, inserted_at);
-- +goose Down
DROP INDEX IF EXISTS site_inserted_index;