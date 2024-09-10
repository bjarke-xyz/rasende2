package repository

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

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/jmoiron/sqlx"
	"github.com/samber/lo"
)

type sqliteNewsRepository struct {
	appContext *core.AppContext
}

func NewSqliteNews(appContext *core.AppContext) core.NewsRepository {
	return &sqliteNewsRepository{appContext: appContext}
}

//go:embed sitedata
var dataFs embed.FS

func (r *sqliteNewsRepository) GetSites(ctx context.Context) ([]core.NewsSite, error) {
	jsonBytes, err := dataFs.ReadFile("sitedata/rss.json")
	if err != nil {
		return nil, fmt.Errorf("could not load rss.json: %w", err)
	}
	var newsSites []core.NewsSite
	err = json.Unmarshal(jsonBytes, &newsSites)
	if err != nil {
		return nil, err
	}
	return newsSites, nil
}

func (r *sqliteNewsRepository) GetSiteNames(ctx context.Context) ([]string, error) {
	rssUrls, err := r.GetSites(ctx)
	if err != nil {
		return nil, err
	}
	siteNames := make([]string, len(rssUrls))
	for i, rssUrl := range rssUrls {
		siteNames[i] = rssUrl.Name
	}
	return siteNames, nil
}

func (r *sqliteNewsRepository) GetRecentItems(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time) ([]core.RssItemDto, error) {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	var rssItems []core.RssItemDto
	args := []interface{}{siteId, limit}
	offsetWhere := ""
	if insertedAtOffset != nil {
		offsetWhere = " AND inserted_at < ? "
		args = []interface{}{siteId, insertedAtOffset, limit}
	}
	sql := fmt.Sprintf("SELECT %v FROM rss_items WHERE site_id = ? "+offsetWhere+" ORDER BY inserted_at DESC LIMIT ?", DBTags(core.RssItemDto{}))
	log.Printf("GetRecentItems: sql=%v", sql)
	err = db.Select(&rssItems, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items for site %v: %w", siteId, err)
	}
	r.EnrichWithSiteNames(ctx, rssItems)
	return rssItems, nil
}
func (r *sqliteNewsRepository) GetRecentItemIds(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time, maxLookBack *time.Time) ([]string, *time.Time, error) {
	db, err := db.Open(r.appContext.Config)
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
	var rssItems []core.RssItemDto
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

func (r *sqliteNewsRepository) GetItemsByIds(ctx context.Context, itemIds []string) ([]core.RssItemDto, error) {
	var rssItems []core.RssItemDto
	if len(itemIds) == 0 {
		return rssItems, nil
	}
	db, err := db.Open(r.appContext.Config)
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
	r.EnrichWithSiteNames(ctx, rssItems)
	return rssItems, nil
}

func (r *sqliteNewsRepository) GetSiteCountByItemIds(ctx context.Context, allItemIds []string) ([]core.SiteCount, error) {
	if len(allItemIds) == 0 {
		return make([]core.SiteCount, 0), nil
	}
	siteCountMap := make(map[int]int, 0)
	log.Printf("GetSiteCountByItemIds allItemIds=%v", len(allItemIds))
	itemIdsChunks := lo.Chunk(allItemIds, 4000)
	db, err := db.Open(r.appContext.Config)
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

	result := make([]core.SiteCount, len(siteCountMap))
	siteCountMapIndex := 0
	for k, v := range siteCountMap {
		siteCount := core.SiteCount{SiteId: k, Count: v}
		result[siteCountMapIndex] = siteCount
		siteCountMapIndex++
	}
	r.EnrichSiteCountWithSiteNames(ctx, result)
	slices.SortFunc(result, func(i, j core.SiteCount) int {
		return cmp.Compare(i.SiteName, j.SiteName)
	})
	return result, nil
}

func (r *sqliteNewsRepository) EnrichOneFakeNewsWithSiteNames(ctx context.Context, fn *core.FakeNewsDto) {
	if fn == nil {
		return
	}
	rssUrls, err := r.GetSites(ctx)
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]core.NewsSite, 0)
		for _, rssUrl := range rssUrls {
			rssUrlsById[rssUrl.Id] = rssUrl
		}
		rssUrl, ok := rssUrlsById[fn.SiteId]
		if ok {
			fn.SiteName = rssUrl.Name
		}
	}
}

func (r *sqliteNewsRepository) EnrichFakeNewsWithSiteNames(ctx context.Context, fakeNews []core.FakeNewsDto) {
	if len(fakeNews) == 0 {
		return
	}
	rssUrls, err := r.GetSites(ctx)
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]core.NewsSite, 0)
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

func (r *sqliteNewsRepository) EnrichSiteCountWithSiteNames(ctx context.Context, siteCounts []core.SiteCount) {
	if len(siteCounts) == 0 {
		return
	}
	rssUrls, err := r.GetSites(ctx)
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]core.NewsSite, 0)
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

func (r *sqliteNewsRepository) EnrichRssSearchResultWithSiteNames(ctx context.Context, rssSearchResults []core.RssSearchResult) {
	if len(rssSearchResults) == 0 {
		return
	}
	rssUrls, err := r.GetSites(ctx)
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]core.NewsSite, 0)
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

func (r *sqliteNewsRepository) EnrichWithSiteNames(ctx context.Context, rssItems []core.RssItemDto) {
	if len(rssItems) == 0 {
		return
	}
	rssUrls, err := r.GetSites(ctx)
	if err == nil && len(rssUrls) > 0 {
		rssUrlsById := make(map[int]core.NewsSite, 0)
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

func (r *sqliteNewsRepository) GetExistingItemsByIds(ctx context.Context, itemIds []string) (map[string]any, error) {
	db, err := db.Open(r.appContext.Config)
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

func (r *sqliteNewsRepository) GetArticleCounts(ctx context.Context) (map[int]int, error) {
	db, err := db.Open(r.appContext.Config)
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

func (r *sqliteNewsRepository) InsertItems(ctx context.Context, rssUrl core.NewsSite, items []core.RssItemDto) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	db, err := db.Open(r.appContext.Config)
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

func (r *sqliteNewsRepository) GetRecentFakeNews(ctx context.Context, limit int, publishedAfter *time.Time) ([]core.FakeNewsDto, error) {
	db, err := db.Open(r.appContext.Config)
	var fakeNewsDtos []core.FakeNewsDto
	if err != nil {
		return fakeNewsDtos, err
	}
	sqlQuery := ""
	var args []any
	orderBySql := "ORDER BY published DESC"
	if publishedAfter != nil {
		sqlQuery = fmt.Sprintf("SELECT %v FROM fake_news WHERE highlighted = 1 AND published < ? %v LIMIT ?", DBTags(core.FakeNewsDto{}), orderBySql)
		args = []any{*publishedAfter, limit}
	} else {
		sqlQuery = fmt.Sprintf("SELECT %v FROM fake_news WHERE highlighted = 1 %v LIMIT ?", DBTags(core.FakeNewsDto{}), orderBySql)
		args = []any{limit}
	}
	log.Printf("GetRecentFakeNews: SQL=%v, args=%v", sqlQuery, args)
	err = db.Select(&fakeNewsDtos, sqlQuery, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fakeNewsDtos, nil
		}
		return fakeNewsDtos, err
	}
	r.EnrichFakeNewsWithSiteNames(ctx, fakeNewsDtos)
	return fakeNewsDtos, nil
}

func (r *sqliteNewsRepository) GetPopularFakeNews(ctx context.Context, limit int, publishedAfter *time.Time, votes int) ([]core.FakeNewsDto, error) {
	db, err := db.Open(r.appContext.Config)
	var fakeNewsDtos []core.FakeNewsDto
	if err != nil {
		return fakeNewsDtos, err
	}
	sqlQuery := ""
	var args []any
	orderBySql := "ORDER BY VOTES desc, published DESC"
	if publishedAfter != nil {
		sqlQuery = fmt.Sprintf("SELECT %v FROM fake_news WHERE highlighted = 1 AND votes <= ? AND (votes < ? OR published < ?) %v LIMIT ?", DBTags(core.FakeNewsDto{}), orderBySql)
		args = []any{votes, votes, *publishedAfter, limit}
	} else {
		sqlQuery = fmt.Sprintf("SELECT %v FROM fake_news WHERE highlighted = 1 %v LIMIT ?", DBTags(core.FakeNewsDto{}), orderBySql)
		args = []any{limit}
	}
	log.Printf("GetPopularFakeNews: SQL=%v, args=%v", sqlQuery, args)
	err = db.Select(&fakeNewsDtos, sqlQuery, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fakeNewsDtos, nil
		}
		return fakeNewsDtos, err
	}
	r.EnrichFakeNewsWithSiteNames(ctx, fakeNewsDtos)
	return fakeNewsDtos, nil
}

func (r *sqliteNewsRepository) GetFakeNews(ctx context.Context, id string) (*core.FakeNewsDto, error) {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return nil, err
	}
	var fakeNewsDto core.FakeNewsDto
	sqlQuery := fmt.Sprintf("SELECT %v FROM fake_news WHERE external_id = ?", DBTags(core.FakeNewsDto{}))
	err = db.Get(&fakeNewsDto, sqlQuery, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.EnrichOneFakeNewsWithSiteNames(ctx, &fakeNewsDto)
	return &fakeNewsDto, nil
}

func (r *sqliteNewsRepository) GetFakeNewsByTitle(ctx context.Context, siteId int, title string) (*core.FakeNewsDto, error) {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return nil, err
	}
	var fakeNewsDto core.FakeNewsDto
	sqlQuery := fmt.Sprintf("SELECT %v FROM fake_news WHERE site_id = ? AND title = ?", DBTags(core.FakeNewsDto{}))
	err = db.Get(&fakeNewsDto, sqlQuery, siteId, title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.EnrichOneFakeNewsWithSiteNames(ctx, &fakeNewsDto)
	return &fakeNewsDto, nil
}

func (r *sqliteNewsRepository) CreateFakeNews(ctx context.Context, siteId int, title string, externalId string) error {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = db.Exec("INSERT INTO fake_news (site_name, title, content, published, site_id, external_id) VALUES (?, ?, ?, ?, ?, ?) on conflict do nothing", "", title, "", now, siteId, externalId)
	if err != nil {
		return fmt.Errorf("error inserting fake news: %w", err)
	}
	return nil
}

func (r *sqliteNewsRepository) UpdateFakeNews(ctx context.Context, siteId int, title string, content string) error {
	db, err := db.Open(r.appContext.Config)
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

func (r *sqliteNewsRepository) SetFakeNewsImgUrl(ctx context.Context, siteId int, title string, imgUrl string) error {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE fake_news SET img_url = ? WHERE site_id = ? AND title = ?", imgUrl, siteId, title)
	if err != nil {
		return fmt.Errorf("error inserting fake news: %w", err)
	}
	return nil
}

func (r *sqliteNewsRepository) SetFakeNewsHighlighted(ctx context.Context, siteId int, title string, highlighted bool) error {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE fake_news SET highlighted = ? WHERE site_id = ? AND title = ?", highlighted, siteId, title)
	if err != nil {
		return fmt.Errorf("error updating fake news highlighted: %w", err)
	}
	return nil
}

func (r *sqliteNewsRepository) ResetFakeNewsContent(ctx context.Context, siteId int, title string) error {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE fake_news SET content = '' WHERE site_id = ? AND title = ?", siteId, title)
	if err != nil {
		return fmt.Errorf("error resetting fake news content: %w", err)
	}
	return nil
}

func (r *sqliteNewsRepository) VoteFakeNews(ctx context.Context, siteId int, title string, votes int) (int, error) {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return 0, err
	}
	sign := "+"
	if votes < 0 {
		sign = "-"
	}
	absVotes := pkg.IntAbs(votes)
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

// DBTags returns a comma-separated string of "db" tags
func DBTags(v interface{}) string {
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
