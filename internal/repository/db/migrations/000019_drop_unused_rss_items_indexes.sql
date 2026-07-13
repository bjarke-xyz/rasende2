-- +goose Up

-- The only query that touches inserted_at (GetRecentItems) also filters on
-- site_id, so it plans to site_inserted_index(site_id, inserted_at). This index
-- was never chosen, and cost 43MB.
DROP INDEX IF EXISTS rss_items_inserted_at;

-- A pure prefix of site_inserted_index(site_id, inserted_at), which serves every
-- site_id lookup on its own. 10MB.
DROP INDEX IF EXISTS site_id_index;

-- Nothing filters or orders by site_name: InsertItems writes '' for the column
-- and the name is filled in at read time from config by EnrichWithSiteNames. 14MB.
DROP INDEX IF EXISTS site_name_index;

-- +goose Down
CREATE INDEX IF NOT EXISTS rss_items_inserted_at ON rss_items(inserted_at);
CREATE INDEX IF NOT EXISTS site_id_index ON rss_items(site_id);
CREATE INDEX IF NOT EXISTS site_name_index ON rss_items(site_name);
