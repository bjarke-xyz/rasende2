-- +goose Up
ALTER TABLE fake_news ADD COLUMN votes int NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS ix_fake_news_highlighted_published_votes ON fake_news(highlighted,published,votes);
-- +goose Down
DROP INDEX IF EXISTS ix_fake_news_highlighted_published_votes;