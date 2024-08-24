-- +goose Up
CREATE INDEX IF NOT EXISTS ix_fake_news_img_url ON fake_news(img_url);
-- +goose Down
DROP INDEX IF EXISTS ix_fake_news_img_url;