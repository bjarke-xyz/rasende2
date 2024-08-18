-- +goose Up
ALTER TABLE rss_items ADD COLUMN site_id INTEGER;
ALTER TABLE fake_news ADD COLUMN site_id INTEGER;

-- +goose Down