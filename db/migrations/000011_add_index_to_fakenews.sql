-- +goose Up
CREATE INDEX IF NOT EXISTS ix_fake_news_highlighted_published ON fake_news(highlighted,published);
-- +goose Down
DROP INDEX IF EXISTS ix_fake_news_highlighted_published;