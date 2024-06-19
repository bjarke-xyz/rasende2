-- +goose Up
create table if not exists rss_items(
    item_id text primary key,
    site_name text not null,
    title text not null,
    content text,
    link text,
    published timestamp
);

CREATE INDEX IF NOT EXISTS site_name_index ON rss_items(site_name);

-- +goose Down
DROP TABLE IF EXISTS rss_items;

DROP INDEX IF EXISTS site_name_index;