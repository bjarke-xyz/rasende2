package news

import (
	"cmp"
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/storage"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type RssService struct {
	context    *core.AppContext
	repository core.NewsRepository
	sanitizer  *bluemonday.Policy
	search     *RssSearch
}

var (
	rssFetchStatusCodes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rasende2_rss_fetch_status_codes",
		Help: "The total number of rss fetch status codes",
	}, []string{
		"status_code", "name", "url",
	})

	rssArticleCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rasende2_rss_article_count",
		Help: "The total number of rss articles, by site name",
	}, []string{
		"name",
	})
)

func NewRssService(context *core.AppContext, repository core.NewsRepository, search *RssSearch) core.NewsService {
	return &RssService{
		context:    context,
		repository: repository,
		sanitizer:  bluemonday.StrictPolicy(),
		search:     search,
	}
}

func (r *RssService) GetIndexPageData(ctx context.Context, l lang.Lang) (*core.IndexPageData, error) {
	query := l.DefaultQuery
	offset := 0
	limit := 10
	searchContent := false
	orderBy := "-published"

	indexPageData := &core.IndexPageData{}

	chartsPromise := pkg.NewPromise(func() (core.ChartsResult, error) {
		chartData, err := r.GetChartData(ctx, l, query)
		return chartData, err
	})

	results, err := r.SearchItems(ctx, l, query, searchContent, offset, limit, orderBy)
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		return &core.IndexPageData{}, err
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	searchResults := core.SearchResult{
		Items: results,
	}
	chartsData, err := chartsPromise.Get()
	if err != nil {
		log.Printf("failed to get charts data: %v", err)
		return &core.IndexPageData{}, err
	}
	indexPageData.SearchResult = &searchResults
	indexPageData.ChartsResult = &chartsData
	return indexPageData, nil
}

// GetChartData builds the two charts for a query. The edition's own word gets
// the editorial titles ("Den seneste uges raserier"); anything else the visitor
// typed gets neutral ones naming the query back to them.
func (r *RssService) GetChartData(ctx context.Context, l lang.Lang, query string) (core.ChartsResult, error) {
	isDefaultQuery := query == l.DefaultQuery

	siteCountPromise := pkg.NewPromise(func() ([]core.SiteCount, error) {
		return r.GetSiteCountForSearchQuery(ctx, l, query, false)
	})

	now := time.Now()
	sevenDaysAgo := now.Add(-time.Hour * 24 * 6)
	tomorrow := now.Add(time.Hour * 24)
	itemCount, err := r.GetItemCountForSearchQuery(ctx, l, query, false, &sevenDaysAgo, &tomorrow, "published")
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		return core.ChartsResult{}, err
	}

	siteCount, err := siteCountPromise.Get()
	if err != nil {
		log.Printf("failed to get site count with query %v: %v", query, err)
		return core.ChartsResult{}, err
	}

	lineTitle := l.T("chart.line.title")
	lineDatasetLabel := l.T("chart.line.dataset")
	doughnutTitle := l.T("chart.pie.title")
	if !isDefaultQuery {
		lineTitle = l.T("chart.line.titleQuery", query)
		lineDatasetLabel = l.T("chart.line.datasetQuery", query)
		doughnutTitle = l.T("chart.pie.titleQuery", query)
	}
	chartsResult := core.ChartsResult{
		Charts: []core.ChartResult{
			core.MakeLineChartFromSearchQueryCount(itemCount, lineTitle, lineDatasetLabel),
			core.MakeDoughnutChartFromSiteCount(siteCount, doughnutTitle),
		},
	}
	return chartsResult, nil
}

func (r *RssService) Initialise(ctx context.Context) {
	// The migration creates rss_items_fts empty. Backfill it once, in the
	// background, so a fresh database becomes searchable without operator action.
	indexEmpty, err := r.search.IsEmpty(ctx)
	if err != nil {
		log.Printf("failed to check search index: %v", err)
	} else if indexEmpty {
		go r.RebuildSearchIndexAndLogError(context.Background())
	}

	err = r.RefreshMetrics(ctx)
	if err != nil {
		log.Printf("error refreshing metrics: %v", err)
	}
}

func (r *RssService) Dispose() {
}

func (r *RssService) RebuildSearchIndexAndLogError(ctx context.Context) {
	if err := r.search.Rebuild(ctx); err != nil {
		log.Printf("error rebuilding search index: %v", err)
	}
}

func (r *RssService) RefreshMetrics(ctx context.Context) error {
	rssUrls, err := r.repository.GetSites(ctx)
	if err != nil {
		return err
	}
	articleCounts, err := r.repository.GetArticleCounts(ctx)
	if err != nil {
		return err
	}
	for _, rssUrl := range rssUrls {
		articleCount, ok := articleCounts[rssUrl.Id]
		if ok {
			rssArticleCount.WithLabelValues(rssUrl.Name).Set(float64(articleCount))
		}
	}
	r.search.RefreshMetrics()
	return nil
}

func getItemId(item *gofeed.Item) string {
	str := item.Title + ":" + item.Link
	bytes := []byte(str)
	hashedBytes := md5.Sum(bytes)
	hashStr := fmt.Sprintf("%x", hashedBytes)
	return hashStr
}

func (r *RssService) convertToDto(feedItem *gofeed.Item, rssUrl core.NewsSite) core.RssItemDto {
	published := feedItem.PublishedParsed
	if published == nil {
		now := time.Now()
		published = &now
	}
	return core.RssItemDto{
		ItemId:    getItemId(feedItem),
		SiteName:  rssUrl.Name,
		Title:     feedItem.Title,
		Content:   strings.TrimSpace(r.sanitizer.Sanitize(feedItem.Content)),
		Link:      feedItem.Link,
		Published: *published,
	}
}
func (r *RssService) GetSiteNames(ctx context.Context) ([]string, error) {
	siteNames, err := r.repository.GetSiteNames(ctx)
	return siteNames, err
}

// GetSiteInfos returns the sites of one edition. An edition only ever shows its
// own sites — they are the sites its search can reach.
func (r *RssService) GetSiteInfos(ctx context.Context, l lang.Lang) ([]core.NewsSite, error) {
	sites, err := r.repository.GetSites(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(sites, func(site core.NewsSite) bool {
		return site.Language != string(l.Code)
	}), nil
}

func (r *RssService) GetSiteInfo(ctx context.Context, siteName string) (*core.NewsSite, error) {
	siteInfos, err := r.repository.GetSites(ctx)
	if err != nil {
		return nil, err
	}
	for _, rssUrl := range siteInfos {
		if rssUrl.Name == siteName {
			return &rssUrl, nil
		}
	}
	return nil, nil
}
func (r *RssService) GetSiteInfoById(ctx context.Context, id int) (*core.NewsSite, error) {
	siteInfos, err := r.repository.GetSites(ctx)
	if err != nil {
		return nil, err
	}
	for _, rssUrl := range siteInfos {
		if rssUrl.Id == id {
			return &rssUrl, nil
		}
	}
	return nil, nil
}

func (r *RssService) SearchItems(ctx context.Context, l lang.Lang, query string, searchContent bool, offset int, limit int, orderBy string) ([]core.RssSearchResult, error) {
	var items []core.RssSearchResult = []core.RssSearchResult{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	items, err := r.search.Search(ctx, string(l.Code), query, searchContent, nil, nil, orderBy, limit, offset)
	if err != nil {
		return items, fmt.Errorf("failed to search: %w", err)
	}
	r.repository.EnrichRssSearchResultWithSiteNames(ctx, items)
	return items, nil
}

func (r *RssService) GetItemCountForSearchQuery(ctx context.Context, l lang.Lang, query string, searchContent bool, start *time.Time, end *time.Time, orderBy string) ([]core.SearchQueryCount, error) {
	searchQueryCounts := make([]core.SearchQueryCount, 0)
	if len(query) > 50 || len(query) <= 2 {
		return searchQueryCounts, nil
	}
	searchQueryCounts, err := r.search.CountByDay(ctx, string(l.Code), query, searchContent, start, end)
	if err != nil {
		return searchQueryCounts, fmt.Errorf("failed to search: %w", err)
	}
	return searchQueryCounts, nil
}

func (r *RssService) GetSiteCountForSearchQuery(ctx context.Context, l lang.Lang, query string, searchContent bool) ([]core.SiteCount, error) {
	var items []core.SiteCount = []core.SiteCount{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	items, err := r.search.CountBySite(ctx, string(l.Code), query, searchContent)
	if err != nil {
		return items, fmt.Errorf("failed to search: %w", err)
	}
	r.repository.EnrichSiteCountWithSiteNames(ctx, items)
	slices.SortFunc(items, func(a, b core.SiteCount) int {
		return cmp.Compare(a.SiteName, b.SiteName)
	})
	return items, nil
}

func (r *RssService) fetchAndSaveNewItemsForSite(ctx context.Context, rssUrl core.NewsSite) error {
	now := time.Now()
	fromFeed, err := r.parse(rssUrl)
	if err != nil {
		return fmt.Errorf("failed to get items from feed %v: %w", rssUrl.Name, err)
	}
	log.Printf("FetchAndSaveNewItems: %v took %v to parse", rssUrl.Name, time.Since(now))

	fromFeedItemIds := make([]string, len(fromFeed))
	for i, fromFeedItem := range fromFeed {
		fromFeedItemIds[i] = fromFeedItem.ItemId
	}

	dbNow := time.Now()
	existingIds, err := r.repository.GetExistingItemsByIds(ctx, fromFeedItemIds)
	if err != nil {
		return fmt.Errorf("failed to get items for %v: %w", rssUrl.Name, err)
	}
	toInsert := make([]core.RssItemDto, 0)
	for _, item := range fromFeed {
		_, exists := existingIds[item.ItemId]
		isBlockedTitle, err := rssUrl.IsBlockedTitle(item.Title)
		if err != nil {
			return fmt.Errorf("error checking if title is blocked: %w", err)
		}
		if !exists && !isBlockedTitle {
			item.InsertedAt = &now
			item.SiteId = rssUrl.Id
			toInsert = append(toInsert, item)
		}
	}

	log.Printf("FetchAndSaveNewItems: %v inserted %v new items. Took %v", rssUrl.Name, len(toInsert), time.Since(dbNow))
	// InsertItems indexes each new row into rss_items_fts in the same transaction.
	articleCount, err := r.repository.InsertItems(ctx, rssUrl, toInsert)
	if err != nil {
		return fmt.Errorf("failed to insert items for %v: %w", rssUrl.Name, err)
	}
	if articleCount > 0 {
		rssArticleCount.WithLabelValues(rssUrl.Name).Set(float64(articleCount))
	}
	return nil
}

func (r *RssService) GetSites(ctx context.Context) ([]core.NewsSite, error) {
	return r.repository.GetSites(ctx)
}

func (r *RssService) FetchAndSaveNewItems(ctx context.Context) error {
	rssUrls, err := r.repository.GetSites(ctx)
	if err != nil {
		return fmt.Errorf("failed to get rss urls: %w", err)
	}
	var wg sync.WaitGroup
	for _, rssUrl := range rssUrls {
		if (len(rssUrl.Urls)) == 0 {
			log.Printf("not getting items for %v: Urls list is empty", rssUrl.Name)
			continue
		}
		wg.Add(1)
		rssUrl := rssUrl
		go func() {
			defer wg.Done()
			siteErr := r.fetchAndSaveNewItemsForSite(ctx, rssUrl)
			if siteErr != nil {
				log.Printf("fetchAndSaveNewItemsForSite failed for %v: %v", rssUrl.Name, siteErr)
			}
		}()
	}
	wg.Wait()
	// No index reconciliation needed: InsertItems indexes each new row in the same
	// transaction that inserts it.
	return nil
}

func (r *RssService) parse(rssUrl core.NewsSite) ([]core.RssItemDto, error) {
	contents, err := r.getContents(rssUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to get content for site %v: %w", rssUrl.Name, err)
	}
	parsed := make([]core.RssItemDto, 0)
	fp := gofeed.NewParser()
	seenIds := make(map[string]bool)
	for _, content := range contents {
		feed, err := fp.ParseString(content)
		if err != nil {
			return nil, fmt.Errorf("failed to parse site %v: %w", rssUrl.Name, err)
		}

		for _, item := range feed.Items {
			dto := r.convertToDto(item, rssUrl)
			_, hasSeen := seenIds[dto.ItemId]
			if !hasSeen {
				parsed = append(parsed, dto)
				seenIds[dto.ItemId] = true
			}
		}
	}
	return parsed, nil

}

var userAgents = map[string]string{
	"chrome": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
}

func (r *RssService) getContents(rssUrl core.NewsSite) ([]string, error) {
	contents := make([]string, 0)
	for _, url := range rssUrl.Urls {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		if rssUrl.UserAgentKey != "" {
			userAgent, ok := userAgents[rssUrl.UserAgentKey]
			if ok {
				req.Header.Set("User-Agent", userAgent)
			}
		}
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error getting %v: %w", url, err)
		}
		rssFetchStatusCodes.WithLabelValues(fmt.Sprintf("%v", resp.StatusCode), rssUrl.Name, url).Inc()
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body of %v: %w", url, err)
		}
		bodyStr := string(body)
		if resp.StatusCode > 299 {
			log.Printf("error getting %v, got status code %v. headers='%v', body='%v'", url, resp.StatusCode, resp.Header, bodyStr)
			return nil, fmt.Errorf("error getting %v, returned error code %v", url, resp.StatusCode)
		}
		contents = append(contents, bodyStr)
	}
	return contents, nil
}

func (r *RssService) GetRecentTitles(ctx context.Context, siteInfo core.NewsSite, limit int, shuffle bool) ([]string, error) {
	items, err := r.repository.GetRecentItems(ctx, siteInfo.Id, limit, nil)
	if err != nil {
		log.Printf("get items failed: %v", err)
		return []string{}, err
	}
	if len(items) == 0 {
		return []string{}, nil
	}
	itemTitles := make([]string, len(items))
	for i, item := range items {
		itemTitles[i] = item.Title
	}
	if shuffle {
		rand.Shuffle(len(itemTitles), func(i, j int) { itemTitles[i], itemTitles[j] = itemTitles[j], itemTitles[i] })
	}
	return itemTitles, nil
}

func (r *RssService) GetRecentItems(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time) ([]core.RssItemDto, error) {
	return r.repository.GetRecentItems(ctx, siteId, limit, insertedAtOffset)
}
func (r *RssService) GetPopularFakeNews(ctx context.Context, limit int, publishedAfter *time.Time, votes int) ([]core.FakeNewsDto, error) {
	return r.repository.GetPopularFakeNews(ctx, limit, publishedAfter, votes)
}
func (r *RssService) GetRecentFakeNews(ctx context.Context, limit int, publishedAfter *time.Time) ([]core.FakeNewsDto, error) {
	return r.repository.GetRecentFakeNews(ctx, limit, publishedAfter)
}
func (r *RssService) GetFakeNews(ctx context.Context, id string) (*core.FakeNewsDto, error) {
	return r.repository.GetFakeNews(ctx, id)
}
func (r *RssService) GetFakeNewsByTitle(ctx context.Context, siteId int, title string) (*core.FakeNewsDto, error) {
	return r.repository.GetFakeNewsByTitle(ctx, siteId, title)
}

func (r *RssService) CreateFakeNews(ctx context.Context, siteId int, title string, externalId string) error {
	return r.repository.CreateFakeNews(ctx, siteId, title, externalId)
}
func (r *RssService) UpdateFakeNews(ctx context.Context, siteId int, title string, content string) error {
	return r.repository.UpdateFakeNews(ctx, siteId, title, content)
}
func (r *RssService) SetFakeNewsImgUrl(ctx context.Context, siteId int, title string, imgUrl string) error {
	return r.repository.SetFakeNewsImgUrl(ctx, siteId, title, imgUrl)
}
func (r *RssService) SetFakeNewsHighlighted(ctx context.Context, siteId int, title string, highlighted bool) error {
	return r.repository.SetFakeNewsHighlighted(ctx, siteId, title, highlighted)
}
func (r *RssService) ResetFakeNewsContent(ctx context.Context, siteId int, title string) error {
	return r.repository.ResetFakeNewsContent(ctx, siteId, title)
}
func (r *RssService) VoteFakeNews(ctx context.Context, siteId int, title string, votes int) (int, error) {
	return r.repository.VoteFakeNews(ctx, siteId, title, votes)
}

func (r *RssService) CleanUpFakeNewsAndLogError(ctx context.Context) {
	err := r.CleanUpFakeNews(ctx)
	if err != nil {
		log.Printf("error in CleanUpFakeNews: %v", err)
	}
}

func (r *RssService) CleanUpFakeNews(ctx context.Context) error {
	const batchSize = 100
	client, err := storage.NewImageClientFromConfig(ctx, r.context.Config)
	if err != nil {
		return err
	}
	db, err := db.Open(r.context.Config)
	if err != nil {
		return err
	}
	bucket := r.context.Config.S3ImageBucket
	publicBaseUrl := r.context.Config.S3ImagePublicBaseUrl
	var continuationToken *string = nil

	_, err = db.ExecContext(ctx, "DELETE FROM fake_news WHERE highlighted = 0")
	if err != nil {
		return fmt.Errorf("failed to delete non-highlighted from fake_news: %w", err)
	}

	for {
		// List objects in S3 bucket with pagination
		listParams := &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String("rasende2/articleimgs"),
			ContinuationToken: continuationToken,
		}

		resp, err := client.ListObjectsV2(ctx, listParams)
		if err != nil {
			return fmt.Errorf("failed to list object: %w", err)
		}

		var batch []imageObject

		for _, item := range resp.Contents {
			batch = append(batch, imageObject{
				key: *item.Key,
				url: publicBaseUrl + "/" + *item.Key,
			})

			// If batch size is reached, process the batch
			if len(batch) >= batchSize {
				err = processBatch(ctx, client, db, bucket, batch)
				if err != nil {
					return fmt.Errorf("failed to process batch: %w", err)
				}
				batch = batch[:0] // Clear the batch
			}
		}

		// Process any remaining items in the last batch
		if len(batch) > 0 {
			err = processBatch(ctx, client, db, bucket, batch)
			if err != nil {
				return fmt.Errorf("failed to process batch: %w", err)
			}
		}

		// Break if there are no more objects to process
		if !*resp.IsTruncated {
			break
		}

		// Update continuation token for the next batch
		continuationToken = resp.NextContinuationToken
	}
	return nil
}

// imageObject pairs the S3 key an object is stored under with the public URL
// fake_news.img_url holds it as. The two are not interchangeable: the row is
// matched by URL, but the delete must address the key. Passing the URL as the
// key deletes nothing and still succeeds, because S3 treats a delete of an
// absent key as a no-op.
type imageObject struct {
	key string
	url string
}

func processBatch(ctx context.Context, client *s3.Client, db *sql.DB, bucket string, batch []imageObject) error {
	// Prepare the SQL query
	placeholders := strings.Repeat("?,", len(batch))
	placeholders = placeholders[:len(placeholders)-1] // Remove the trailing comma

	query := fmt.Sprintf("SELECT img_url FROM fake_news WHERE img_url IN (%s)", placeholders)

	// Convert batch to a slice of interfaces{} for the query
	args := make([]interface{}, len(batch))
	for i, v := range batch {
		args[i] = v.url
	}

	// Execute the query
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("failed to get img urls from db: %w", err)
	}
	defer rows.Close()

	// Collect existing URLs
	existingURLs := make(map[string]any)
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return fmt.Errorf("failed to scan url: %w", err)
		}
		existingURLs[url] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to read img urls: %w", err)
	}

	// Identify and delete orphaned S3 objects
	for _, obj := range batch {
		if _, ok := existingURLs[obj.url]; !ok {
			_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(obj.key),
			})
			if err != nil {
				log.Println("Failed to delete", obj.key, ":", err)
			} else {
				log.Println("Deleted", obj.key)
			}
		}
	}
	return nil
}
