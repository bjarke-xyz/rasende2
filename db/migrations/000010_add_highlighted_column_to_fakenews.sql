-- +goose Up
ALTER TABLE fake_news ADD COLUMN highlighted boolean NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS ix_fake_news_highlighted ON fake_news(highlighted);
-- +goose Down
DROP INDEX IF EXISTS ix_fake_news_highlighted;