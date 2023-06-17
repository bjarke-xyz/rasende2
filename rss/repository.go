package rss

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/jmoiron/sqlx"
)

type RssRepository struct {
	context *pkg.AppContext
}

func NewRssRepository(context *pkg.AppContext) *RssRepository {
	return &RssRepository{
		context: context,
	}
}

type RssUrlDto struct {
	Name string   `json:"name"`
	Urls []string `json:"urls"`
}

//go:embed data
var dataFs embed.FS

func (r *RssRepository) GetRssUrls() ([]RssUrlDto, error) {
	jsonBytes, err := dataFs.ReadFile("data/rss.json")
	if err != nil {
		return nil, fmt.Errorf("could not load rss.json: %w", err)
	}
	var rssUrls []RssUrlDto
	err = json.Unmarshal(jsonBytes, &rssUrls)
	if err != nil {
		return nil, err
	}
	return rssUrls, nil
}

type RssItemDto struct {
	ItemId    string    `db:"item_id" json:"itemId"`
	SiteName  string    `db:"site_name" json:"siteName"`
	Title     string    `db:"title" json:"title"`
	Content   string    `db:"content" json:"content"`
	Link      string    `db:"link" json:"link"`
	Published time.Time `db:"published" json:"published"`
}

func (r *RssRepository) SearchItems(query string, searchContent bool, offset int, limit int, after *time.Time) ([]RssItemDto, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	var rssItems []RssItemDto
	args := make([]interface{}, 0)
	args = append(args, query)
	sql := "SELECT * FROM rss_items WHERE ( ts_title @@ to_tsquery('danish', $1)"
	if searchContent {
		sql = sql + " OR ts_content @@ to_tsquery('danish', $1) )"
	} else {
		sql = sql + " )" // Close where
	}
	if after != nil {
		sql = sql + " AND published > $2"
		args = append(args, *after)
	}
	sql = sql + " ORDER BY published DESC"
	sql = sql + fmt.Sprintf(" OFFSET %v LIMIT %v", offset, limit)
	// err = db.Select(&rssItems, "SELECT * FROM rss_items WHERE LOWER(title) LIKE '%' || $1 || '%' order by published desc", query)
	log.Printf("SearchItems SQL: %v -- args: %v", sql, len(args))
	err = db.Select(&rssItems, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items with query %v: %w", query, err)
	}
	return rssItems, nil
}

func (r *RssRepository) GetItems(siteName string) ([]RssItemDto, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	var rssItems []RssItemDto
	err = db.Select(&rssItems, "SELECT * FROM rss_items WHERE site_name = $1", siteName)
	if err != nil {
		return nil, fmt.Errorf("error getting items for site %v: %w", siteName, err)
	}
	return rssItems, nil
}

func (r *RssRepository) GetItemsByIds(siteName string, itemIds []string) ([]RssItemDto, error) {
	var rssItems []RssItemDto
	if len(itemIds) == 0 {
		return rssItems, nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	query, args, err := sqlx.In("SELECT * FROM rss_items WHERE site_name = ? AND item_id IN (?)", siteName, itemIds)
	if err != nil {
		return nil, fmt.Errorf("error doing sqlx in for site %v: %w", siteName, err)
	}
	query = db.Rebind(query)
	err = db.Select(&rssItems, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id for site %v: %w", siteName, err)
	}
	return rssItems, nil
}

func (r *RssRepository) GetItemCount(siteName string) (int, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return 0, err
	}
	var count int
	err = db.Get(&count, "SELECT count(*) FROM rss_items WHERE site_name = $1", siteName)
	if err != nil {
		return 0, fmt.Errorf("failed to get item count for site %v: %w", siteName, err)
	}
	return count, nil
}

func (r *RssRepository) InsertItems(items []RssItemDto) error {
	if len(items) == 0 {
		return nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	_, err = db.NamedExec("INSERT INTO rss_items (item_id, site_name, title, content, link, published) "+
		"values (:item_id, :site_name, :title, :content, :link, :published) on conflict do nothing", items)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert: %w", err)
	}
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit tx: %w", err)
	}
	return nil

}
