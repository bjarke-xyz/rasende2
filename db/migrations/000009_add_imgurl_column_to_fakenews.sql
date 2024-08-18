-- +goose Up
ALTER TABLE fake_news ADD COLUMN img_url text;