-- +goose Up
CREATE INDEX IF NOT EXISTS site_id_index ON rss_items(site_id);
CREATE INDEX IF NOT EXISTS fake_news_site_id_index ON fake_news(site_id);

-- +goose Down
DROP INDEX IF EXISTS site_id_index;
DROP INDEX IF EXISTS fake_news_site_id_index;