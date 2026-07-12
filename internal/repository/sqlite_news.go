package repository

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/search"
	"github.com/bjarke-xyz/rasende2/pkg"
)

type sqliteNewsRepository struct {
	appContext *core.AppContext
}

func NewSqliteNews(appContext *core.AppContext) core.NewsRepository {
	return &sqliteNewsRepository{appContext: appContext}
}

//go:embed sitedata
var dataFs embed.FS

// Column lists, in the order the scan helpers below read them.
const (
	rssItemColumns  = "item_id, site_name, title, content, link, published, inserted_at, site_id"
	fakeNewsColumns = "site_name, title, content, published, site_id, img_url, highlighted, votes, external_id"
)

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanRssItem reads rssItemColumns. content and link are nullable in the schema
// but plain strings on the DTO, so a NULL becomes "".
func scanRssItem(scanner rowScanner) (core.RssItemDto, error) {
	var item core.RssItemDto
	var content, link *string
	err := scanner.Scan(&item.ItemId, &item.SiteName, &item.Title, &content, &link,
		&item.Published, &item.InsertedAt, &item.SiteId)
	if err != nil {
		return item, err
	}
	if content != nil {
		item.Content = *content
	}
	if link != nil {
		item.Link = *link
	}
	return item, nil
}

// scanFakeNews reads fakeNewsColumns. published and site_id are nullable.
func scanFakeNews(scanner rowScanner) (core.FakeNewsDto, error) {
	var fakeNews core.FakeNewsDto
	var published *time.Time
	var siteId *int64
	err := scanner.Scan(&fakeNews.SiteName, &fakeNews.Title, &fakeNews.Content, &published, &siteId,
		&fakeNews.ImageUrl, &fakeNews.Highlighted, &fakeNews.Votes, &fakeNews.ExternalId)
	if err != nil {
		return fakeNews, err
	}
	if published != nil {
		fakeNews.Published = *published
	}
	if siteId != nil {
		fakeNews.SiteId = int(*siteId)
	}
	return fakeNews, nil
}

func scanFakeNewsRows(rows *sql.Rows) ([]core.FakeNewsDto, error) {
	defer rows.Close()
	var fakeNewsDtos []core.FakeNewsDto
	for rows.Next() {
		fakeNews, err := scanFakeNews(rows)
		if err != nil {
			return nil, fmt.Errorf("error scanning fake news: %w", err)
		}
		fakeNewsDtos = append(fakeNewsDtos, fakeNews)
	}
	return fakeNewsDtos, rows.Err()
}

// placeholders renders "?, ?, ?" for an IN clause of n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// loadSites parses the embedded rss.json. The file cannot change at runtime, and
// GetSites is on the search hot path — every search enriches its results and its
// site counts with site names — so parse it once.
//
// It also rejects a site that belongs to no edition. That has to fail here, at
// startup, because everything downstream — the analyzer that stems its items,
// the prompt that writes its fake news — takes the language on trust. The
// alternative is a panic later, in a background fetch or halfway through a
// request.
var loadSites = sync.OnceValues(func() ([]core.NewsSite, error) {
	jsonBytes, err := dataFs.ReadFile("sitedata/rss.json")
	if err != nil {
		return nil, fmt.Errorf("could not load rss.json: %w", err)
	}
	var newsSites []core.NewsSite
	if err := json.Unmarshal(jsonBytes, &newsSites); err != nil {
		return nil, err
	}
	for _, site := range newsSites {
		if _, ok := lang.Get(site.Language); !ok {
			return nil, fmt.Errorf("site %q (id %v) has language %q, which is not one of the editions", site.Name, site.Id, site.Language)
		}
	}
	return newsSites, nil
})

// GetSites returns the configured news sites. The slice is cloned because the
// backing one is shared by every caller: sorting or reordering the result would
// otherwise corrupt it for everyone.
func (r *sqliteNewsRepository) GetSites(ctx context.Context) ([]core.NewsSite, error) {
	sites, err := loadSites()
	if err != nil {
		return nil, err
	}
	return slices.Clone(sites), nil
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
	var rssItems []core.RssItemDto
	args := []any{siteId, limit}
	offsetWhere := ""
	if insertedAtOffset != nil {
		offsetWhere = " AND inserted_at < ? "
		args = []any{siteId, insertedAtOffset, limit}
	}
	sqlQuery := "SELECT " + rssItemColumns + " FROM rss_items WHERE site_id = ? " + offsetWhere + " ORDER BY inserted_at DESC LIMIT ?"
	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items for site %v: %w", siteId, err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanRssItem(rows)
		if err != nil {
			return nil, fmt.Errorf("error scanning item for site %v: %w", siteId, err)
		}
		rssItems = append(rssItems, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error getting items for site %v: %w", siteId, err)
	}
	r.EnrichWithSiteNames(ctx, rssItems)
	return rssItems, nil
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
	result := make(map[string]any, len(itemIds))
	if len(itemIds) == 0 {
		return result, nil
	}
	args := make([]any, len(itemIds))
	for i, itemId := range itemIds {
		args[i] = itemId
	}
	rows, err := db.QueryContext(ctx, "SELECT item_id FROM rss_items WHERE item_id IN ("+placeholders(len(itemIds))+")", args...)
	if err != nil {
		return nil, fmt.Errorf("error getting items by id: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var itemId string
		if err := rows.Scan(&itemId); err != nil {
			return nil, fmt.Errorf("error scanning item id: %w", err)
		}
		result[itemId] = struct{}{}
	}
	return result, rows.Err()
}

func (r *sqliteNewsRepository) GetArticleCounts(ctx context.Context) (map[int]int, error) {
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT site_id, article_count FROM site_count")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]int, 0)
	for rows.Next() {
		var siteId int
		var articleCount int
		if err := rows.Scan(&siteId, &articleCount); err != nil {
			return nil, fmt.Errorf("error scanning: %w", err)
		}
		result[siteId] = articleCount
	}
	return result, rows.Err()
}

func (r *sqliteNewsRepository) InsertItems(ctx context.Context, rssUrl core.NewsSite, items []core.RssItemDto) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	db, err := db.Open(r.appContext.Config)
	if err != nil {
		return 0, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin tx: %w", err)
	}
	// Insert one row at a time so that RowsAffected tells us which items were new:
	// "on conflict do nothing" makes a batch insert unable to report that. Each new
	// row is indexed in this same transaction, which is what keeps rss_items_fts
	// from ever drifting out of step with rss_items.
	for _, item := range items {
		result, err := tx.ExecContext(ctx, "INSERT INTO rss_items (item_id, site_name, title, content, link, published, inserted_at, site_id) "+
			"values (?, '', ?, ?, ?, ?, ?, ?) on conflict do nothing",
			item.ItemId, item.Title, item.Content, item.Link, item.Published, item.InsertedAt, item.SiteId)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to insert: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to get rows affected: %w", err)
		}
		if inserted == 0 {
			continue
		}
		id, err := result.LastInsertId()
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to get inserted id: %w", err)
		}
		// Stemmed in the site's language: an item has no language of its own, it
		// inherits the one of the site that published it.
		if _, err := tx.ExecContext(ctx, search.InsertFtsSQL, id,
			search.StemText(rssUrl.Language, item.Title), search.StemText(rssUrl.Language, item.Content)); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to index item %v: %w", item.ItemId, err)
		}
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, "INSERT INTO site_count (site_id, article_count, updated_at) VALUES (?, ?, ?) on conflict do update set article_count = article_count + excluded.article_count, updated_at = excluded.updated_at", rssUrl.Id, len(items), now)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to insert site count: %w", err)
	}
	var articleCount int
	err = tx.QueryRowContext(ctx, "SELECT article_count FROM site_count WHERE site_id = ?", rssUrl.Id).Scan(&articleCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return 0, fmt.Errorf("error getting article count: %w", err)
	}
	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("failed to commit tx: %w", err)
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
		sqlQuery = "SELECT " + fakeNewsColumns + " FROM fake_news WHERE highlighted = 1 AND published < ? " + orderBySql + " LIMIT ?"
		args = []any{*publishedAfter, limit}
	} else {
		sqlQuery = "SELECT " + fakeNewsColumns + " FROM fake_news WHERE highlighted = 1 " + orderBySql + " LIMIT ?"
		args = []any{limit}
	}
	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return fakeNewsDtos, err
	}
	fakeNewsDtos, err = scanFakeNewsRows(rows)
	if err != nil {
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
		sqlQuery = "SELECT " + fakeNewsColumns + " FROM fake_news WHERE highlighted = 1 AND votes <= ? AND (votes < ? OR published < ?) " + orderBySql + " LIMIT ?"
		args = []any{votes, votes, *publishedAfter, limit}
	} else {
		sqlQuery = "SELECT " + fakeNewsColumns + " FROM fake_news WHERE highlighted = 1 " + orderBySql + " LIMIT ?"
		args = []any{limit}
	}
	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return fakeNewsDtos, err
	}
	fakeNewsDtos, err = scanFakeNewsRows(rows)
	if err != nil {
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
	sqlQuery := "SELECT " + fakeNewsColumns + " FROM fake_news WHERE external_id = ?"
	fakeNewsDto, err := scanFakeNews(db.QueryRowContext(ctx, sqlQuery, id))
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
	sqlQuery := "SELECT " + fakeNewsColumns + " FROM fake_news WHERE site_id = ? AND title = ?"
	fakeNewsDto, err := scanFakeNews(db.QueryRowContext(ctx, sqlQuery, siteId, title))
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("error starting vote tx: %w", err)
	}
	query := fmt.Sprintf("UPDATE fake_news SET votes = votes %v ? WHERE site_id = ? AND title = ?", sign)
	_, err = tx.ExecContext(ctx, query, absVotes, siteId, title)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error updating fake news votes: %w", err)
	}
	var updatedVotes int
	err = tx.QueryRowContext(ctx, "SELECT votes FROM fake_news WHERE site_id = ? AND title = ?", siteId, title).Scan(&updatedVotes)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error getting updated votes: %w", err)
	}
	if updatedVotes < 0 {
		_, err = tx.ExecContext(ctx, "UPDATE fake_news SET votes = 0 WHERE site_id = ? and title = ?", siteId, title)
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
