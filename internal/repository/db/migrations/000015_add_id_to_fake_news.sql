-- +goose Up
ALTER TABLE fake_news ADD COLUMN external_id TEXT;
