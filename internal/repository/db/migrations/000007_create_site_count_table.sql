-- +goose Up
CREATE TABLE IF NOT EXISTS site_count(
    site_id INT PRIMARY KEY,
    article_count INT,
    updated_at TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS site_count;