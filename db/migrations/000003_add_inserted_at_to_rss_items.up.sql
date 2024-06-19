-- +goose Up
ALTER TABLE rss_items ADD COLUMN inserted_at timestamp;

CREATE INDEX IF NOT EXISTS rss_items_inserted_at ON rss_items(inserted_at);