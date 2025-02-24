package news

import (
	"cmp"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/storage"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/jmoiron/sqlx"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/samber/lo"
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

const CacheKeyIndexPage = "PAGE:INDEX"

func (r *RssService) cacheKeyIndexPage() string {
	return CacheKeyIndexPage + ":" + r.context.Config.AppEnv
}

func (r *RssService) GetIndexPageData(ctx context.Context, nocache bool) (*core.IndexPageData, error) {
	query := "rasende"
	offset := 0
	limit := 10
	searchContent := false
	orderBy := "-published"

	cacheKey := r.cacheKeyIndexPage()
	indexPageData := &core.IndexPageData{}
	if !nocache {
		fromCache, _ := r.context.Infra.Cache.GetObj(cacheKey, indexPageData)
		if fromCache {
			log.Printf("got %v from cache", cacheKey)
			return indexPageData, nil
		}
	}

	chartsPromise := pkg.NewPromise(func() (core.ChartsResult, error) {
		chartData, err := r.GetChartData(ctx, query)
		return chartData, err
	})

	results, err := r.SearchItems(ctx, query, searchContent, offset, limit, orderBy)
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		return &core.IndexPageData{}, err
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	searchResults := core.SearchResult{
		HighlightedWords: []string{query},
		Items:            results,
	}
	chartsData, err := chartsPromise.Get()
	if err != nil {
		log.Printf("failed to get charts data: %v", err)
		return &core.IndexPageData{}, err
	}
	indexPageData.SearchResult = &searchResults
	indexPageData.ChartsResult = &chartsData
	go r.context.Infra.Cache.InsertObj(cacheKey, indexPageData, 120)
	log.Printf("key %v missed cache", cacheKey)
	return indexPageData, nil
}

func (r *RssService) GetChartData(ctx context.Context, query string) (core.ChartsResult, error) {
	isRasende := query == "rasende"

	siteCountPromise := pkg.NewPromise(func() ([]core.SiteCount, error) {
		return r.GetSiteCountForSearchQuery(ctx, query, false)
	})

	now := time.Now()
	sevenDaysAgo := now.Add(-time.Hour * 24 * 6)
	tomorrow := now.Add(time.Hour * 24)
	itemCount, err := r.GetItemCountForSearchQuery(ctx, query, false, &sevenDaysAgo, &tomorrow, "published")
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		return core.ChartsResult{}, err
	}

	siteCount, err := siteCountPromise.Get()
	if err != nil {
		log.Printf("failed to get site count with query %v: %v", query, err)
		return core.ChartsResult{}, err
	}

	lineTitle := "Den seneste uges raserier"
	lineDatasetLabel := "Raseriudbrud"
	doughnutTitle := "Raseri i de forskellige medier"
	if !isRasende {
		lineTitle = "Den seneste uges brug af '" + query + "'"
		lineDatasetLabel = "Antal '" + query + "'"
		doughnutTitle = "Brug af '" + query + "' i de forskellige medier"
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
	indexCreated, err := r.search.OpenAndCreateIndexIfNotExists()
	if err != nil {
		log.Printf("failed to open/create index: %v", err)
	}
	if indexCreated {
		go r.AddMissingItemsToSearchIndexAndLogError(context.Background(), nil)
	}

	err = r.RefreshMetrics(ctx)
	if err != nil {
		log.Printf("error refreshing metrics: %v", err)
	}
}

func (r *RssService) Dispose() {
	r.search.CloseIndex()
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

func (r *RssService) GetSiteInfos(ctx context.Context) ([]core.NewsSite, error) {
	return r.repository.GetSites(ctx)
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

func (r *RssService) SearchItems(ctx context.Context, query string, searchContent bool, offset int, limit int, orderBy string) ([]core.RssSearchResult, error) {
	var items []core.RssSearchResult = []core.RssSearchResult{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	searchResult, err := r.search.Search(ctx, query, limit, offset, nil, nil, orderBy, searchContent, []string{"title", "content", "published", "siteId", "link"})
	if err != nil {
		return items, fmt.Errorf("failed to search: %w", err)
	}
	// itemIds := make([]string, len(searchResult.Hits))
	items = make([]core.RssSearchResult, searchResult.Total)
	for i, doc := range searchResult.Hits {
		item := core.RssSearchResult{
			ItemId: doc.ID,
		}
		for k, field := range doc.Fields {
			switch field := field.(type) {
			case string:
				switch k {
				case "title":
					item.Title = field
				case "content":
					item.Content = field
				case "link":
					item.Link = field
				case "published":
					_published, err := time.Parse(time.RFC3339, field)
					if err != nil {
						return items, fmt.Errorf("error parsing published '%s' to time: %w", field, err)
					}
					item.Published = _published
				default:
					log.Println("default case for field", k, field)
				}
			case float64:
				item.SiteId = int(field)
			}
			items[i] = item
		}
	}
	r.repository.EnrichRssSearchResultWithSiteNames(ctx, items)
	return items, err
}

func (r *RssService) GetItemCountForSearchQuery(ctx context.Context, query string, searchContent bool, start *time.Time, end *time.Time, orderBy string) ([]core.SearchQueryCount, error) {
	searchQueryCounts := make([]core.SearchQueryCount, 0)
	if len(query) > 50 || len(query) <= 2 {
		return searchQueryCounts, nil
	}

	searchQueryCountMap := make(map[time.Time]int, 0)
	searchResult, err := r.search.Search(ctx, query, math.MaxInt, 0, start, end, orderBy, searchContent, []string{"published"})
	if err != nil {
		return searchQueryCounts, fmt.Errorf("failed to search: %w", err)
	}
	for _, doc := range searchResult.Hits {
		timestampInterface := doc.Fields["published"]
		timestampStr, ok := timestampInterface.(string)
		if !ok {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			log.Printf("error parsing date %v: %v", timestampStr, err)
			continue
		}
		dayTimestamp := timestamp.Truncate(24 * time.Hour)
		currentCount, ok := searchQueryCountMap[dayTimestamp]
		if ok {
			searchQueryCountMap[dayTimestamp] = currentCount + 1
		} else {
			searchQueryCountMap[dayTimestamp] = 1
		}
	}

	for k, v := range searchQueryCountMap {
		searchQueryCount := core.SearchQueryCount{Timestamp: k, Count: v}
		searchQueryCounts = append(searchQueryCounts, searchQueryCount)
	}
	slices.SortFunc(searchQueryCounts, func(a, b core.SearchQueryCount) int {
		return cmp.Compare(a.Timestamp.Unix(), b.Timestamp.Unix())
	})

	return searchQueryCounts, nil
}

func (r *RssService) GetSiteCountForSearchQuery(ctx context.Context, query string, searchContent bool) ([]core.SiteCount, error) {

	var items []core.SiteCount = []core.SiteCount{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	searchResult, err := r.search.Search(ctx, query, math.MaxInt, 0, nil, nil, "_score", searchContent, []string{"siteId"})
	if err != nil {
		return items, fmt.Errorf("failed to search: %w", err)
	}
	countMap := make(map[int]int, 0)
	for _, doc := range searchResult.Hits {
		siteIdInterface, ok := doc.Fields["siteId"]
		if !ok {
			continue
		}
		siteIdFloat, ok := siteIdInterface.(float64)
		if !ok {
			continue
		}
		siteId := int(siteIdFloat)
		currentCount, ok := countMap[siteId]
		if ok {
			countMap[siteId] = currentCount + 1
		} else {
			countMap[siteId] = 1
		}
	}
	for k, v := range countMap {
		siteCount := core.SiteCount{SiteId: k, Count: v}
		items = append(items, siteCount)
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
		if !exists && !rssUrl.IsBlockedTitle(item.Title) {
			item.InsertedAt = &now
			item.SiteId = rssUrl.Id
			toInsert = append(toInsert, item)
		}
	}

	log.Printf("FetchAndSaveNewItems: %v inserted %v new items. Took %v", rssUrl.Name, len(toInsert), time.Since(dbNow))
	articleCount, err := r.repository.InsertItems(ctx, rssUrl, toInsert)
	if err != nil {
		return fmt.Errorf("failed to insert items for %v: %w", rssUrl.Name, err)
	}
	err = r.search.Index(toInsert)
	if err != nil {
		log.Printf("failed to index items: %v", err)
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
	now := time.Now()
	oneMonthAgo := now.Add(-time.Hour * 24 * 31)
	go r.AddMissingItemsToSearchIndexAndLogError(context.Background(), &oneMonthAgo)
	go r.context.Infra.Cache.DeleteExpired()
	go r.context.Infra.Cache.DeleteByPrefix(r.cacheKeyIndexPage())
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

func (r *RssService) BackupDbAndLogError(ctx context.Context) error {
	err := r.BackupDb(ctx)
	if err != nil {
		log.Printf("failed to backup db: %v", err)
		err = r.NotifyBackupDbError(ctx, err)
		if err != nil {
			log.Printf("failed to send notification about err: %v", err)
		}
	}
	return nil
}

func (r *RssService) NotifyBackupDbError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	msg := "rasende2 failed to backup: " + err.Error()
	reader := strings.NewReader(msg)
	resp, err := http.Post("https://ntfy.sh/"+r.context.Config.NtfyTopic, "text/plain", reader)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("got non-200 status code from ntfy: %v", resp.StatusCode)
	}
	return nil
}

// var dbSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
// 	Name: "rasende2_db_size_bytes",
// 	Help: "Size in bytes of rasende2 db (measured at backup time)",
// })

func (r *RssService) BackupDb(ctx context.Context) error {
	return fmt.Errorf("not implemented")
	// err := r.repository.BackupDb(ctx)
	// if err != nil {
	// 	return fmt.Errorf("failed to backup db: %w", err)
	// }
	// dbBackupFile, err := os.Open(r.context.Config.BackupDbPath)
	// if err != nil {
	// 	return fmt.Errorf("failed to open backup db file: %w", err)
	// }
	// dbBackupFileStat, err := dbBackupFile.Stat()
	// if err != nil {
	// 	return fmt.Errorf("failed to stat db backup file: %w", err)
	// }
	// dbSizeGauge.Set(float64(dbBackupFileStat.Size()))
	// r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
	// 	return aws.Endpoint{
	// 		URL: r.context.Config.S3BackupUrl,
	// 	}, nil
	// })
	// cfg, err := config.LoadDefaultConfig(ctx,
	// 	config.WithEndpointResolverWithOptions(r2Resolver),
	// 	config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(r.context.Config.S3BackupAccessKeyId, r.context.Config.S3BackupSecretAccessKey, "")),
	// 	config.WithRegion("auto"),
	// )
	// if err != nil {
	// 	return fmt.Errorf("failed to load r2 config")
	// }

	// client := s3.NewFromConfig(cfg)

	// bucket := r.context.Config.S3BackupBucket
	// key := "rasende2/db-backup.db"

	// objects, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
	// 	Bucket: &bucket,
	// })
	// if err != nil {
	// 	return fmt.Errorf("failed to list r2 objects: %w", err)
	// }
	// if len(objects.Contents) > 0 {
	// 	existingObject := objects.Contents[0]
	// 	objFound := false
	// 	for _, obj := range objects.Contents {
	// 		if obj.Key != nil && *obj.Key == key {
	// 			existingObject = obj
	// 			objFound = true
	// 			break
	// 		}
	// 	}
	// 	// do not attempt to over-write a larger file with a smaller file
	// 	if objFound && existingObject.Size != nil && *existingObject.Size > dbBackupFileStat.Size() {
	// 		return fmt.Errorf("attemping to over-write large file (%v) in r2, with small local file (%v)", *existingObject.Size, dbBackupFileStat.Size())
	// 	}
	// }

	// _, err = client.PutObject(ctx, &s3.PutObjectInput{
	// 	Bucket: &bucket,
	// 	Key:    &key,
	// 	Body:   dbBackupFile,
	// })
	// if err != nil {
	// 	return fmt.Errorf("failed to upload db backup file: %w", err)
	// }

	// err = dbBackupFile.Close()
	// if err != nil {
	// 	return fmt.Errorf("failed to close db backup file: %w", err)
	// }

	// err = os.Remove(r.context.Config.BackupDbPath)
	// if err != nil {
	// 	return fmt.Errorf("failed to remove local db backup file: %w", err)
	// }

	// return nil
}

func (r *RssService) AddMissingItemsToSearchIndexAndLogError(ctx context.Context, maxLookBack *time.Time) {
	err := r.addMissingItemsToSearchIndex(ctx, maxLookBack)
	if err != nil {
		log.Printf("failed to add missing items to search index: %v", err)
	}
	go r.context.Infra.Cache.DeleteExpired()
	go r.context.Infra.Cache.DeleteByPrefix(r.cacheKeyIndexPage())
}

func (r *RssService) addMissingItemsToSearchIndex(ctx context.Context, maxLookBack *time.Time) error {
	rssUrls, err := r.repository.GetSites(ctx)
	if err != nil {
		return fmt.Errorf("error getting rss urls: %w", err)
	}
	for _, rssUrl := range rssUrls {
		log.Printf("adding missing items to search from site %v", rssUrl.Name)
		err = r.addMissingItemsToSearchIndexForSite(ctx, rssUrl, maxLookBack)
		if err != nil {
			return fmt.Errorf("error adding missing items to search index for site %v: %w", rssUrl.Name, err)
		}
	}
	return nil
}

func (r *RssService) addMissingItemsToSearchIndexForSite(ctx context.Context, rssUrl core.NewsSite, maxLookBack *time.Time) error {
	chunkSize := 10000
	limit := chunkSize
	var insertedAtOffset *time.Time
	getMore := true
	for getMore {
		rssItemIds, lastInsertedAt, err := r.repository.GetRecentItemIds(ctx, rssUrl.Id, limit, insertedAtOffset, maxLookBack)
		if err != nil {
			return fmt.Errorf("error getting recent item ids for site %v: %w", rssUrl.Id, err)
		}
		if len(rssItemIds) < chunkSize {
			getMore = false
		} else {
			insertedAtOffset = lastInsertedAt
		}
		rssItemIdsToIndex := make([]string, 0)
		itemsInIndex, err := r.search.HasItems(ctx, rssItemIds)
		if err != nil {
			return fmt.Errorf("error checking if search index has items, site %v: %w", rssUrl.Id, err)
		}
		for _, itemId := range rssItemIds {
			_, ok := itemsInIndex[itemId]
			if !ok {
				rssItemIdsToIndex = append(rssItemIdsToIndex, itemId)
			}
		}
		log.Printf("Out of %v db items, %v were not in search index", len(rssItemIds), len(rssItemIdsToIndex))
		if len(rssItemIdsToIndex) > 0 {
			err = r.indexItemIds(ctx, rssItemIdsToIndex, rssUrl)
			if err != nil {
				return fmt.Errorf("error indexing item ids: %w", err)
			}
		}
	}
	return nil
}

func (r *RssService) indexItemIds(ctx context.Context, allItemIds []string, rssUrl core.NewsSite) error {
	if len(allItemIds) == 0 {
		return nil
	}
	chunkSize := 3000
	if rssUrl.ArticleHasContent {
		chunkSize = 100
	}
	itemIdChunks := lo.Chunk(allItemIds, chunkSize)
	for _, itemIds := range itemIdChunks {
		items, err := r.repository.GetItemsByIds(ctx, itemIds)
		if err != nil {
			return fmt.Errorf("error getting items: %w", err)
		}
		err = r.search.Index(items)
		if err != nil {
			return fmt.Errorf("error indexing: %w", err)
		}
	}
	return nil
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

		var batch []string

		for _, item := range resp.Contents {
			imgUrl := "https://static.bjarke.xyz/" + *item.Key
			batch = append(batch, imgUrl)

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

func processBatch(ctx context.Context, client *s3.Client, db *sqlx.DB, bucket string, batch []string) error {
	// Prepare the SQL query
	placeholders := strings.Repeat("?,", len(batch))
	placeholders = placeholders[:len(placeholders)-1] // Remove the trailing comma

	query := fmt.Sprintf("SELECT img_url FROM fake_news WHERE img_url IN (%s)", placeholders)

	// Convert batch to a slice of interfaces{} for the query
	args := make([]interface{}, len(batch))
	for i, v := range batch {
		args[i] = v
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

	// Identify and delete orphaned S3 objects
	for _, key := range batch {
		if _, ok := existingURLs[key]; !ok {
			_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
			})
			if err != nil {
				log.Println("Failed to delete", key, ":", err)
			} else {
				log.Println("Deleted", key)
			}
		}
	}
	return nil
}
