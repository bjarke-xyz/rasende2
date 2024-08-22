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
	"reflect"
	"slices"
	"strings"
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
	DescriptionEn     string   `json:"descriptionEn"`
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
	SiteName    string    `db:"site_name" json:"siteName"`
	Title       string    `db:"title" json:"title"`
	Content     string    `db:"content" json:"content"`
	Published   time.Time `db:"published" json:"published"`
	SiteId      int       `db:"site_id" json:"siteId"`
	ImageUrl    *string   `db:"img_url" json:"imageUrl"`
	Highlighted bool      `db:"highlighted" json:"highlighted"`
	Votes       int       `db:"votes" json:"votes"`
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
	sql := fmt.Sprintf("SELECT %v FROM rss_items WHERE site_id = ? "+offsetWhere+" ORDER BY inserted_at DESC LIMIT ?", getDBTags(RssItemDto{}))
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

func (r *RssRepository) EnrichOneFakeNewsWithSiteNames(fn *FakeNewsDto) {
	if fn == nil {
		return
	}
	rssUrls, err := r.GetRssUrls()
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]RssUrlDto, 0)
		for _, rssUrl := range rssUrls {
			rssUrlsById[rssUrl.Id] = rssUrl
		}
		rssUrl, ok := rssUrlsById[fn.SiteId]
		if ok {
			fn.SiteName = rssUrl.Name
		}
	}
}

func (r *RssRepository) EnrichFakeNewsWithSiteNames(fakeNews []FakeNewsDto) {
	if len(fakeNews) == 0 {
		return
	}
	rssUrls, err := r.GetRssUrls()
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]RssUrlDto, 0)
		for _, rssUrl := range rssUrls {
			rssUrlsById[rssUrl.Id] = rssUrl
		}
		for i, fn := range fakeNews {
			rssUrl, ok := rssUrlsById[fn.SiteId]
			if ok {
				fn.SiteName = rssUrl.Name
				fakeNews[i] = fn
			}
		}
	}
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
	sqlQuery := fmt.Sprintf("SELECT %v FROM rss_items WHERE item_id IN (?)", getDBTags(RssItemDto{}))
	query, args, err := sqlx.In(sqlQuery, itemIds)
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
	var articleCounts []int
	err = tx.Select(&articleCounts, "SELECT article_count FROM site_count WHERE site_id = ?", rssUrl.Id)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error getting article count: %w", err)
	}
	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("failed to commit tx: %w", err)
	}
	articleCount := 0
	if len(articleCounts) > 0 {
		articleCount = articleCounts[0]
	}
	return articleCount, nil

}

func (r *RssRepository) GetHighlightedFakeNews(limit int, publishedAfter *time.Time, votes int) ([]FakeNewsDto, error) {
	db, err := db.Open(r.context.Config)
	var fakeNewsDtos []FakeNewsDto
	if err != nil {
		return fakeNewsDtos, err
	}
	sqlQuery := ""
	var args []any
	orderBySql := "ORDER BY VOTES desc, published DESC"
	if publishedAfter != nil {
		sqlQuery = fmt.Sprintf("SELECT %v FROM fake_news WHERE highlighted = 1 AND votes <= ? AND (votes < ? OR published < ?) %v LIMIT ?", getDBTags(FakeNewsDto{}), orderBySql)
		args = []any{votes, votes, *publishedAfter, limit}
	} else {
		sqlQuery = fmt.Sprintf("SELECT %v FROM fake_news WHERE highlighted = 1 %v LIMIT ?", getDBTags(FakeNewsDto{}), orderBySql)
		args = []any{limit}
	}
	log.Printf("GetHighlightedFakeNews: SQL=%v, args=%v", sqlQuery, args)
	err = db.Select(&fakeNewsDtos, sqlQuery, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fakeNewsDtos, nil
		}
		return fakeNewsDtos, err
	}
	r.EnrichFakeNewsWithSiteNames(fakeNewsDtos)
	return fakeNewsDtos, nil
}

func (r *RssRepository) GetFakeNews(siteId int, title string) (*FakeNewsDto, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return nil, err
	}
	var fakeNewsDto FakeNewsDto
	sqlQuery := fmt.Sprintf("SELECT %v FROM fake_news WHERE site_id = ? and title = ?", getDBTags(FakeNewsDto{}))
	err = db.Get(&fakeNewsDto, sqlQuery, siteId, title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.EnrichOneFakeNewsWithSiteNames(&fakeNewsDto)
	return &fakeNewsDto, nil
}

func (r *RssRepository) CreateFakeNews(siteId int, title string) error {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
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
	now := time.Now().UTC()
	_, err = db.Exec("UPDATE fake_news SET content = ?, published = ? WHERE site_id = ? AND title = ?", content, now, siteId, title)
	if err != nil {
		return fmt.Errorf("error inserting fake news: %w", err)
	}
	return nil
}

func (r *RssRepository) SetFakeNewsImgUrl(siteId int, title string, imgUrl string) error {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE fake_news SET img_url = ? WHERE site_id = ? AND title = ?", imgUrl, siteId, title)
	if err != nil {
		return fmt.Errorf("error inserting fake news: %w", err)
	}
	return nil
}

func (r *RssRepository) SetFakeNewsHighlighted(siteId int, title string, highlighted bool) error {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE fake_news SET highlighted = ? WHERE site_id = ? AND title = ?", highlighted, siteId, title)
	if err != nil {
		return fmt.Errorf("error updating fake news highlighted: %w", err)
	}
	return nil
}

func (r *RssRepository) ResetFakeNewsContent(siteId int, title string) error {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE fake_news SET content = '' WHERE site_id = ? AND title = ?", siteId, title)
	if err != nil {
		return fmt.Errorf("error resetting fake news content: %w", err)
	}
	return nil
}

func (r *RssRepository) VoteFakeNews(siteId int, title string, votes int) (int, error) {
	db, err := db.Open(r.context.Config)
	if err != nil {
		return 0, err
	}
	sign := "+"
	if votes < 0 {
		sign = "-"
	}
	absVotes := IntAbs(votes)
	tx, err := db.Beginx()
	if err != nil {
		return 0, fmt.Errorf("error starting vote tx: %w", err)
	}
	query := fmt.Sprintf("UPDATE fake_news SET votes = votes %v ? WHERE site_id = ? AND title = ?", sign)
	_, err = tx.Exec(query, absVotes, siteId, title)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error updating fake news votes: %w", err)
	}
	var updatedVotes int
	err = tx.Get(&updatedVotes, "SELECT votes FROM fake_news WHERE site_id = ? AND title = ?", siteId, title)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error getting updated votes: %w", err)
	}
	if updatedVotes < 0 {
		_, err = tx.Exec("UPDATE fake_news SET votes = 0 WHERE site_id = ? and title = ?", siteId, title)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("error updating votes to 0 after they were negative: %w", err)
		}
	}
	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("error commiting votes tx: %w", err)
	}
	return updatedVotes, nil
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

func IntAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Function to get comma-separated "db" tags
func getDBTags(v interface{}) string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem() // Get the element type if it's a pointer
	}

	var tags []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		dbTag := field.Tag.Get("db")
		if dbTag != "" {
			tags = append(tags, dbTag)
		}
	}
	return strings.Join(tags, ", ")
}
