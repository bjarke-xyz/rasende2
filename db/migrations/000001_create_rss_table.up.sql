CREATE TABLE IF NOT EXISTS rss_items (
    item_id VARCHAR(400) PRIMARY KEY,
    site_name VARCHAR(255) NOT NULL,
    title TEXT NOT NULL,
    content TEXT,
    link VARCHAR(400),
    published TIMESTAMP
);

CREATE FULLTEXT INDEX ix_rss_items_fulltext ON rss_items(title, content);

CREATE FULLTEXT INDEX ix_rss_items_title_fulltext ON rss_items(title);

CREATE INDEX ix_rss_items_site_name ON rss_items(site_name);