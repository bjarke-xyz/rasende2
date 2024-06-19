package rss

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/jmoiron/sqlx"
	"github.com/mattn/go-sqlite3"
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
	Name        string   `json:"name"`
	Urls        []string `json:"urls"`
	Description string   `json:"description"`
	Id          int      `json:"id"`
	Disabled    bool     `json:"disabled"`
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
	ItemId     string     `db:"item_id" json:"itemId"`
	SiteName   string     `db:"site_name" json:"siteName"`
	Title      string     `db:"title" json:"title"`
	Content    string     `db:"content" json:"content"`
	Link       string     `db:"link" json:"link"`
	Published  time.Time  `db:"published" json:"published"`
	InsertedAt *time.Time `db:"inserted_at" json:"insertedAt"`
	SiteId     int        `db:"site_id" json:"siteId"`
}

type FakeNewsDto struct {
	SiteName  string    `db:"site_name" json:"siteName"`
	Title     string    `db:"title" json:"title"`
	Content   string    `db:"content" json:"content"`
	Published time.Time `db:"published" json:"published"`
	SiteId    int       `db:"site_id" json:"siteId"`
}

func (r *RssRepository) GetSiteNames() ([]string, error) {
	rssUrls, err := r.GetRssUrls()
	if err != nil {
		return nil, err
	}
	siteNames := make([]string, len(rssUrls))
	for i, rssUrl := range rssUrls {
		siteNames[i] = rssUrl.Name
	}
	return siteNames, nil
}

func (r *RssRepository) GetRecentItems(ctx context.Context, siteId int, offset int, limit int) ([]RssItemDto, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	var rssItems []RssItemDto
	err = db.Select(&rssItems, "SELECT * FROM rss_items WHERE site_id = ? ORDER BY inserted_at LIMIT ? OFFSET ?", siteId, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("error getting items for site %v: %w", siteId, err)
	}
	r.EnrichWithSiteNames(rssItems)
	return rssItems, nil
}
func (r *RssRepository) GetRecentItemIds(ctx context.Context, siteId int, offset int, limit int) ([]string, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	var rssItemIds []string
	err = db.Select(&rssItemIds, "SELECT item_id FROM rss_items WHERE site_id = ? ORDER BY inserted_at LIMIT ? OFFSET ?", siteId, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("error getting item ids for site %v: %w", siteId, err)
	}
	return rssItemIds, nil
}

func (r *RssRepository) GetItemsByIds(itemIds []string) ([]RssItemDto, error) {
	var rssItems []RssItemDto
	if len(itemIds) == 0 {
		return rssItems, nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	inArgs := []interface{}{itemIds}
	query, args, err := sqlx.In("SELECT item_id, title, content, link, published, inserted_at, site_id FROM rss_items WHERE item_id IN (?)", inArgs...)
	if err != nil {
		return nil, fmt.Errorf("error doing sqlx in: %w", err)
	}
	query = db.Rebind(query)
	err = db.Select(&rssItems, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id: %w", err)
	}
	r.EnrichWithSiteNames(rssItems)
	return rssItems, nil
}

func (r *RssRepository) GetItemsByIdsWithOrder(itemIds []string, after *time.Time, orderBy string) ([]RssItemDto, error) {
	var rssItems []RssItemDto
	if len(itemIds) == 0 {
		return rssItems, nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	inArgs := []interface{}{itemIds}
	afterStr := ""
	if after != nil {
		afterStr = " AND published > ?"
		inArgs = append(inArgs, after)
	}
	descAsc := "ASC"
	if orderBy[0] == '-' {
		descAsc = "DESC"
		orderBy = orderBy[1:]
	}
	orderByStr := " ORDER BY " + orderBy + " " + descAsc
	query, args, err := sqlx.In("SELECT item_id, title, content, link, published, inserted_at, site_id FROM rss_items WHERE item_id IN (?)"+afterStr+orderByStr, inArgs...)
	if err != nil {
		return nil, fmt.Errorf("error doing sqlx in with order: %w", err)
	}
	// log.Printf("GetItemsByIds-- QUERY:%v", query)
	// log.Printf("GetItemsByIds-- ARGS:%v", args)
	query = db.Rebind(query)
	err = db.Select(&rssItems, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id with order: %w", err)
	}
	r.EnrichWithSiteNames(rssItems)
	return rssItems, nil
}

func (r *RssRepository) EnrichWithSiteNames(rssItems []RssItemDto) {
	if len(rssItems) == 0 {
		return
	}
	rssUrls, err := r.GetRssUrls()
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]RssUrlDto, 0)
		for _, rssUrl := range rssUrls {
			rssUrlsById[rssUrl.Id] = rssUrl
		}
		for i, rssItem := range rssItems {
			rssUrl, ok := rssUrlsById[rssItem.SiteId]
			if ok {
				rssItem.SiteName = rssUrl.Name
				rssItems[i] = rssItem
			}
		}
	}
}

func (r *RssRepository) GetExistingItemsBySiteAndIds(itemIds []string) (map[string]any, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	query, args, err := sqlx.In("SELECT item_id FROM rss_items WHERE item_id IN (?)", itemIds)
	if err != nil {
		return nil, fmt.Errorf("error doing sqlx in: %w", err)
	}
	query = db.Rebind(query)
	dbItemIds := make([]string, 0)
	err = db.Select(&dbItemIds, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id: %w", err)
	}
	result := make(map[string]any, len(dbItemIds))
	for _, itemId := range dbItemIds {
		result[itemId] = struct{}{}
	}
	return result, nil
}

func (r *RssRepository) GetItemsByNameAndIds(itemIds []string) ([]RssItemDto, error) {
	var rssItems []RssItemDto
	if len(itemIds) == 0 {
		return rssItems, nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	query, args, err := sqlx.In("SELECT * FROM rss_items WHERE item_id IN (?)", itemIds)
	if err != nil {
		return nil, fmt.Errorf("error doing sqlx in: %w", err)
	}
	query = db.Rebind(query)
	err = db.Select(&rssItems, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id: %w", err)
	}
	return rssItems, nil
}

func (r *RssRepository) GetItemCount(siteId int) (int, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return 0, err
	}
	var count int
	err = db.Get(&count, "SELECT count(*) FROM rss_items WHERE site_id = ?", siteId)
	if err != nil {
		return 0, fmt.Errorf("failed to get item count for site %v: %w", siteId, err)
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
	_, err = db.NamedExec("INSERT INTO rss_items (item_id, site_name, title, content, link, published, inserted_at, site_id) "+
		"values (:item_id, '', :title, :content, :link, :published, :inserted_at, :site_id) on conflict do nothing", items)
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

func (r *RssRepository) GetFakeNews(siteId int, title string) (*FakeNewsDto, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	var fakeNewsDto FakeNewsDto
	err = db.Get(&fakeNewsDto, "SELECT * FROM fake_news WHERE site_id = ? and title = ?", siteId, title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &fakeNewsDto, nil
}

func (r *RssRepository) CreateFakeNews(siteId int, title string) error {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = db.Exec("INSERT INTO fake_news (site_name, title, content, published, site_id) VALUES (?, ?, ?, ?, ?) on conflict do nothing", "", title, "", now, siteId)
	if err != nil {
		return fmt.Errorf("error inserting fake news: %w", err)
	}
	return nil
}

func (r *RssRepository) UpdateFakeNews(siteId int, title string, content string) error {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = db.Exec("UPDATE fake_news SET content = ?, published = ? WHERE site_id = ? AND title = ?", content, now, siteId, title)
	if err != nil {
		return fmt.Errorf("error inserting fake news: %w", err)
	}
	return nil
}

func (r *RssRepository) BackupDb(ctx context.Context) error {
	// https://rbn.im/backing-up-a-SQLite-database-with-Go/backing-up-a-SQLite-database-with-Go.html
	destDb, err := sql.Open("sqlite3", r.context.Config.BackupDbPath)
	if err != nil {
		return err
	}
	srcDb, err := sql.Open("sqlite3", r.context.Config.DbConnStr)
	if err != nil {
		return err
	}
	destConn, err := destDb.Conn(ctx)
	if err != nil {
		return err
	}

	srcConn, err := srcDb.Conn(ctx)
	if err != nil {
		return err
	}

	return destConn.Raw(func(destConn interface{}) error {
		return srcConn.Raw(func(srcConn interface{}) error {
			destSQLiteConn, ok := destConn.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("can't convert destination connection to SQLiteConn")
			}

			srcSQLiteConn, ok := srcConn.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("can't convert source connection to SQLiteConn")
			}

			b, err := destSQLiteConn.Backup("main", srcSQLiteConn, "main")
			if err != nil {
				return fmt.Errorf("error initializing SQLite backup: %w", err)
			}

			done, err := b.Step(-1)
			if !done {
				return fmt.Errorf("step of -1, but not done")
			}
			if err != nil {
				return fmt.Errorf("error in stepping backup: %w", err)
			}

			err = b.Finish()
			if err != nil {
				return fmt.Errorf("error finishing backup: %w", err)
			}

			return err
		})
	})

}
