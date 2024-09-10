-- +goose Up
CREATE INDEX IF NOT EXISTS ix_fake_news_external_id ON fake_news(external_id);

-- +goose Down
DROP INDEX IF EXISTS ix_fake_news_external_id;