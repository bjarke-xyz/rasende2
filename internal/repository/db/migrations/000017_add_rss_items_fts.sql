-- +goose Up

-- rss_items keyed only by "item_id text primary key" has an *implicit* rowid,
-- which a bare VACUUM is free to renumber. rss_items_fts is joined on that rowid,
-- so a VACUUM would silently desync the search index. An explicit INTEGER PRIMARY
-- KEY is a rowid alias, and those are never renumbered.
CREATE TABLE rss_items_new(
    id INTEGER PRIMARY KEY,
    item_id TEXT NOT NULL UNIQUE,
    site_name TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT,
    link TEXT,
    published TIMESTAMP,
    inserted_at TIMESTAMP,
    site_id INTEGER
);

INSERT INTO rss_items_new (id, item_id, site_name, title, content, link, published, inserted_at, site_id)
SELECT rowid, item_id, site_name, title, content, link, published, inserted_at, site_id FROM rss_items;

DROP TABLE rss_items;
ALTER TABLE rss_items_new RENAME TO rss_items;

-- DROP TABLE took the old indexes with it.
CREATE INDEX IF NOT EXISTS site_name_index ON rss_items(site_name);
CREATE INDEX IF NOT EXISTS rss_items_inserted_at ON rss_items(inserted_at);
CREATE INDEX IF NOT EXISTS site_id_index ON rss_items(site_id);
CREATE INDEX IF NOT EXISTS site_inserted_index ON rss_items(site_id, inserted_at);

-- Contentless: the index stores terms only, never the text. rss_items.content holds
-- full article bodies, so duplicating them here would roughly double the database.
-- Rows are keyed by rowid = rss_items.id and hydrated by joining back to rss_items.
--
-- The stored terms are Danish-stemmed by internal/search before insertion, so
-- unicode61 only ever splits an already-analyzed token stream on whitespace.
-- remove_diacritics 0 leaves æøå alone; the Go analyzer owns all normalization.
CREATE VIRTUAL TABLE rss_items_fts USING fts5(
    title,
    content,
    content='',
    contentless_delete=1,
    tokenize="unicode61 remove_diacritics 0"
);

-- +goose Down
DROP TABLE IF EXISTS rss_items_fts;

CREATE TABLE rss_items_old(
    item_id text primary key,
    site_name text not null,
    title text not null,
    content text,
    link text,
    published timestamp,
    inserted_at timestamp,
    site_id INTEGER
);

INSERT INTO rss_items_old (item_id, site_name, title, content, link, published, inserted_at, site_id)
SELECT item_id, site_name, title, content, link, published, inserted_at, site_id FROM rss_items;

DROP TABLE rss_items;
ALTER TABLE rss_items_old RENAME TO rss_items;

CREATE INDEX IF NOT EXISTS site_name_index ON rss_items(site_name);
CREATE INDEX IF NOT EXISTS rss_items_inserted_at ON rss_items(inserted_at);
CREATE INDEX IF NOT EXISTS site_id_index ON rss_items(site_id);
CREATE INDEX IF NOT EXISTS site_inserted_index ON rss_items(site_id, inserted_at);
