package rss

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/jmoiron/sqlx"
	"github.com/mattn/go-sqlite3"
	"github.com/samber/lo"
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
	Name              string   `json:"name"`
	Urls              []string `json:"urls"`
	Description       string   `json:"description"`
	Id                int      `json:"id"`
	Disabled          bool     `json:"disabled"`
	ArticleHasContent bool     `json:"articleHasContent"`
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

func (r *RssRepository) GetRecentItems(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time) ([]RssItemDto, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	var rssItems []RssItemDto
	args := []interface{}{siteId, limit}
	offsetWhere := ""
	if insertedAtOffset != nil {
		offsetWhere = " AND inserted_at < ? "
		args = []interface{}{siteId, insertedAtOffset, limit}
	}
	sql := "SELECT * FROM rss_items WHERE site_id = ? " + offsetWhere + " ORDER BY inserted_at DESC LIMIT ?"
	err = db.Select(&rssItems, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items for site %v: %w", siteId, err)
	}
	r.EnrichWithSiteNames(rssItems)
	return rssItems, nil
}
func (r *RssRepository) GetRecentItemIds(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time, maxLookBack *time.Time) ([]string, *time.Time, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, nil, err
	}
	args := []interface{}{siteId, limit}
	offsetWhere := ""
	if insertedAtOffset != nil {
		offsetWhere = " AND inserted_at < ? "
		args = []interface{}{siteId, insertedAtOffset, limit}
	}
	maxLookBackWhere := ""
	if maxLookBack != nil {
		maxLookBackWhere = " AND inserted_at > ? "
		// TODO: find better way of doing this...
		if offsetWhere != "" {
			args = []interface{}{siteId, insertedAtOffset, maxLookBack, limit}
		} else {
			args = []interface{}{siteId, maxLookBack, limit}
		}
	}
	var rssItems []RssItemDto
	sql := "SELECT item_id, inserted_at FROM rss_items WHERE site_id = ? " + offsetWhere + maxLookBackWhere + " ORDER BY inserted_at DESC LIMIT ?"
	log.Println(sql, args)
	err = db.Select(&rssItems, sql, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting item ids for site %v: %w", siteId, err)
	}
	itemIds := make([]string, len(rssItems))
	var lastInsertedAt *time.Time
	for i, rssItem := range rssItems {
		itemIds[i] = rssItem.ItemId
		if i == len(rssItems)-1 {
			lastInsertedAt = rssItem.InsertedAt
		}
	}
	return itemIds, lastInsertedAt, nil
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

func (r *RssRepository) GetSiteCountByItemIds(allItemIds []string) ([]SiteCount, error) {
	if len(allItemIds) == 0 {
		return make([]SiteCount, 0), nil
	}
	siteCountMap := make(map[int]int, 0)
	log.Printf("GetSiteCountByItemIds allItemIds=%v", len(allItemIds))
	itemIdsChunks := lo.Chunk(allItemIds, 4000)
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	for _, itemIds := range itemIdsChunks {
		inArgs := []interface{}{itemIds}
		query, args, err := sqlx.In("select site_id, count(*) as site_count from rss_items where item_id IN (?) group by site_id", inArgs...)
		if err != nil {
			return nil, fmt.Errorf("error doing sqlx in for site count: %w", err)
		}
		query = db.Rebind(query)
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("error getting items by id with order: %w", err)
		}
		for rows.Next() {
			var siteId int
			var siteCount int
			err = rows.Scan(&siteId, &siteCount)
			if err != nil {
				return nil, fmt.Errorf("error scanning site count: %w", err)
			}
			currentCount, ok := siteCountMap[siteId]
			if ok {
				siteCountMap[siteId] = currentCount + siteCount
			} else {
				siteCountMap[siteId] = siteCount
			}
		}
	}

	result := make([]SiteCount, len(siteCountMap))
	siteCountMapIndex := 0
	for k, v := range siteCountMap {
		siteCount := SiteCount{SiteId: k, Count: v}
		result[siteCountMapIndex] = siteCount
		siteCountMapIndex++
	}
	r.EnrichSiteCountWithSiteNames(result)
	slices.SortFunc(result, func(i, j SiteCount) int {
		return cmp.Compare(i.SiteName, j.SiteName)
	})
	return result, nil
}

func (r *RssRepository) GetItemsByIdsWithOrder(allItemIds []string, orderBy string) ([]RssItemDto, error) {
	var rssItems []RssItemDto
	if len(allItemIds) == 0 {
		return rssItems, nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	inArgs := []interface{}{allItemIds}
	descAsc := "ASC"
	if orderBy[0] == '-' {
		descAsc = "DESC"
		orderBy = orderBy[1:]
	}
	orderByStr := " ORDER BY " + orderBy + " " + descAsc
	query, args, err := sqlx.In("SELECT item_id, title, content, link, published, inserted_at, site_id FROM rss_items WHERE item_id IN (?)"+orderByStr, inArgs...)
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

func (r *RssRepository) EnrichSiteCountWithSiteNames(siteCounts []SiteCount) {
	if len(siteCounts) == 0 {
		return
	}
	rssUrls, err := r.GetRssUrls()
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]RssUrlDto, 0)
		for _, rssUrl := range rssUrls {
			rssUrlsById[rssUrl.Id] = rssUrl
		}
		for i, siteCount := range siteCounts {
			rssUrl, ok := rssUrlsById[siteCount.SiteId]
			if ok {
				siteCount.SiteName = rssUrl.Name
				siteCounts[i] = siteCount
			}
		}
	}
}

func (r *RssRepository) EnrichRssSearchResultWithSiteNames(rssSearchResults []RssSearchResult) {
	if len(rssSearchResults) == 0 {
		return
	}
	rssUrls, err := r.GetRssUrls()
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]RssUrlDto, 0)
		for _, rssUrl := range rssUrls {
			rssUrlsById[rssUrl.Id] = rssUrl
		}
		for i, rssItem := range rssSearchResults {
			rssUrl, ok := rssUrlsById[rssItem.SiteId]
			if ok {
				rssItem.SiteName = rssUrl.Name
				rssSearchResults[i] = rssItem
			}
		}
	}
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

func (r *RssRepository) GetExistingItemsByIds(itemIds []string) (map[string]any, error) {
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
	var siteCounts []RssItemDto
	if len(itemIds) == 0 {
		return siteCounts, nil
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
	err = db.Select(&siteCounts, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id: %w", err)
	}
	return siteCounts, nil
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

func (r *RssRepository) GetArticleCounts() (map[int]int, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	rows, err := db.Queryx("SELECT site_id, article_count FROM site_count")
	if err != nil {
		return nil, err
	}
	result := make(map[int]int, 0)
	for rows.Next() {
		var siteId int
		var articleCount int
		err = rows.Scan(&siteId, &articleCount)
		if err != nil {
			return nil, fmt.Errorf("error scanning: %w", err)
		}
		result[siteId] = articleCount
	}
	return result, nil
}

func (r *RssRepository) InsertItems(rssUrl RssUrlDto, items []RssItemDto) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return 0, err
	}

	tx, err := db.Beginx()
	if err != nil {
		return 0, fmt.Errorf("failed to begin tx: %w", err)
	}
	_, err = tx.NamedExec("INSERT INTO rss_items (item_id, site_name, title, content, link, published, inserted_at, site_id) "+
		"values (:item_id, '', :title, :content, :link, :published, :inserted_at, :site_id) on conflict do nothing", items)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to insert: %w", err)
	}
	now := time.Now().UTC()
	_, err = tx.Exec("INSERT INTO site_count (site_id, article_count, updated_at) VALUES (?, ?, ?) on conflict do update set article_count = article_count + excluded.article_count, updated_at = excluded.updated_at", rssUrl.Id, len(items), now)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to insert site count: %w", err)
	}
	var articleCount int
	err = tx.Select(&articleCount, "SELECT article_count FROM site_count WHERE site_id = ?", rssUrl.Id)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error getting article count: %w", err)
	}
	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("failed to commit tx: %w", err)
	}
	return articleCount, nil

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
