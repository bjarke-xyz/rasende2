package rss

import (
	"cmp"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/samber/lo"
)

type SiteCount struct {
	SiteId   int    `json:"siteId"`
	SiteName string `json:"siteName"`
	Count    int    `json:"count"`
}

type SearchQueryCount struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int       `json:"count"`
}

type RssSearchResult struct {
	ItemId    string    `json:"itemId"`
	SiteName  string    `json:"siteName"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Link      string    `json:"link"`
	Published time.Time `json:"published"`
	SiteId    int       `json:"siteId"`
}

type RssService struct {
	context    *pkg.AppContext
	repository *RssRepository
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

func NewRssService(context *pkg.AppContext, repository *RssRepository, search *RssSearch) *RssService {
	return &RssService{
		context:    context,
		repository: repository,
		sanitizer:  bluemonday.StrictPolicy(),
		search:     search,
	}
}

func (r *RssService) RefreshMetrics() error {
	rssUrls, err := r.repository.GetRssUrls()
	if err != nil {
		return err
	}
	articleCounts, err := r.repository.GetArticleCounts()
	if err != nil {
		return err
	}
	for _, rssUrl := range rssUrls {
		articleCount, ok := articleCounts[rssUrl.Id]
		if ok {
			rssArticleCount.WithLabelValues(rssUrl.Name).Set(float64(articleCount))
		}
	}
	return nil
}

func getItemId(item *gofeed.Item) string {
	str := item.Title + ":" + item.Link
	bytes := []byte(str)
	hashedBytes := md5.Sum(bytes)
	hashStr := fmt.Sprintf("%x", hashedBytes)
	return hashStr
}

func (r *RssService) convertToDto(feedItem *gofeed.Item, rssUrl RssUrlDto) RssItemDto {
	published := feedItem.PublishedParsed
	if published == nil {
		now := time.Now()
		published = &now
	}
	return RssItemDto{
		ItemId:    getItemId(feedItem),
		SiteName:  rssUrl.Name,
		Title:     feedItem.Title,
		Content:   strings.TrimSpace(r.sanitizer.Sanitize(feedItem.Content)),
		Link:      feedItem.Link,
		Published: *published,
	}
}
func (r *RssService) GetSiteNames() ([]string, error) {
	siteNames, err := r.repository.GetSiteNames()
	return siteNames, err
}

func (r *RssService) GetSiteInfo(siteName string) (*RssUrlDto, error) {
	siteInfos, err := r.repository.GetRssUrls()
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

func (r *RssService) SearchItems(ctx context.Context, query string, searchContent bool, offset int, limit int, orderBy string) ([]RssSearchResult, error) {
	var items []RssSearchResult = []RssSearchResult{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	searchResult, err := r.search.Search(ctx, query, limit, offset, nil, nil, orderBy, searchContent, []string{"title", "content", "published", "siteId", "link"})
	if err != nil {
		return items, fmt.Errorf("failed to search: %w", err)
	}
	// itemIds := make([]string, len(searchResult.Hits))
	items = make([]RssSearchResult, searchResult.Total)
	for i, doc := range searchResult.Hits {
		item := RssSearchResult{
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
	r.repository.EnrichRssSearchResultWithSiteNames(items)
	return items, err
}

func (r *RssService) GetItemCountForSearchQuery(ctx context.Context, query string, searchContent bool, start *time.Time, end *time.Time, orderBy string) ([]SearchQueryCount, error) {
	searchQueryCounts := make([]SearchQueryCount, 0)
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
		searchQueryCount := SearchQueryCount{Timestamp: k, Count: v}
		searchQueryCounts = append(searchQueryCounts, searchQueryCount)
	}
	slices.SortFunc(searchQueryCounts, func(a, b SearchQueryCount) int {
		return cmp.Compare(a.Timestamp.Unix(), b.Timestamp.Unix())
	})

	return searchQueryCounts, nil
}

func (r *RssService) GetSiteCountForSearchQuery(ctx context.Context, query string, searchContent bool) ([]SiteCount, error) {

	var items []SiteCount = []SiteCount{}
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
		siteCount := SiteCount{SiteId: k, Count: v}
		items = append(items, siteCount)
	}
	r.repository.EnrichSiteCountWithSiteNames(items)
	slices.SortFunc(items, func(a, b SiteCount) int {
		return cmp.Compare(a.SiteName, b.SiteName)
	})
	return items, nil
}

func (r *RssService) fetchAndSaveNewItemsForSite(rssUrl RssUrlDto) error {
	fromFeed, err := r.parse(rssUrl)
	if err != nil {
		return fmt.Errorf("failed to get items from feed %v: %w", rssUrl.Name, err)
	}

	now := time.Now()
	fromFeedItemIds := make([]string, len(fromFeed))
	for i, fromFeedItem := range fromFeed {
		fromFeedItemIds[i] = fromFeedItem.ItemId
	}

	existingIds, err := r.repository.GetExistingItemsByIds(fromFeedItemIds)
	if err != nil {
		return fmt.Errorf("failed to get items for %v: %w", rssUrl.Name, err)
	}
	toInsert := make([]RssItemDto, 0)
	for _, item := range fromFeed {
		_, exists := existingIds[item.ItemId]
		if !exists {
			item.InsertedAt = &now
			item.SiteId = rssUrl.Id
			toInsert = append(toInsert, item)
		}
	}

	log.Printf("FetchAndSaveNewItems: %v inserted %v new items", rssUrl.Name, len(toInsert))
	articleCount, err := r.repository.InsertItems(rssUrl, toInsert)
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

func (r *RssService) GetRssUrls() ([]RssUrlDto, error) {
	return r.repository.GetRssUrls()
}

func (r *RssService) FetchAndSaveNewItems() error {
	rssUrls, err := r.repository.GetRssUrls()
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
			siteErr := r.fetchAndSaveNewItemsForSite(rssUrl)
			if siteErr != nil {
				log.Printf("fetchAndSaveNewItemsForSite failed for %v: %v", rssUrl.Name, siteErr)
			}
		}()
	}
	wg.Wait()
	now := time.Now()
	oneMonthAgo := now.Add(-time.Hour * 24 * 31)
	go r.AddMissingItemsToSearchIndexAndLogError(context.Background(), &oneMonthAgo)
	go r.context.Cache.DeleteExpired()
	return nil
}

func (r *RssService) parse(rssUrl RssUrlDto) ([]RssItemDto, error) {
	contents, err := r.getContents(rssUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to get content for site %v: %w", rssUrl.Name, err)
	}
	parsed := make([]RssItemDto, 0)
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

func (r *RssService) getContents(rssUrl RssUrlDto) ([]string, error) {
	contents := make([]string, 0)
	for _, url := range rssUrl.Urls {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error getting %v: %w", url, err)
		}
		rssFetchStatusCodes.WithLabelValues(fmt.Sprintf("%v", resp.StatusCode), rssUrl.Name, url).Inc()
		if resp.StatusCode > 299 {
			return nil, fmt.Errorf("error getting %v, returned error code %v", url, resp.StatusCode)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body of %v: %w", url, err)
		}
		bodyStr := string(body)
		contents = append(contents, bodyStr)
	}
	return contents, nil
}

func (r *RssService) GetFakeNews(siteId int, title string) (*FakeNewsDto, error) {
	return r.repository.GetFakeNews(siteId, title)
}

func (r *RssService) CreateFakeNews(siteId int, title string) error {
	return r.repository.CreateFakeNews(siteId, title)
}
func (r *RssService) UpdateFakeNews(siteId int, title string, content string) error {
	return r.repository.UpdateFakeNews(siteId, title, content)
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
	msg := "rasende2-api failed to backup: " + err.Error()
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

var dbSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "rasende2_db_size_bytes",
	Help: "Size in bytes of rasende2 db (measured at backup time)",
})

func (r *RssService) BackupDb(ctx context.Context) error {
	err := r.repository.BackupDb(ctx)
	if err != nil {
		return fmt.Errorf("failed to backup db: %w", err)
	}
	dbBackupFile, err := os.Open(r.context.Config.BackupDbPath)
	if err != nil {
		return fmt.Errorf("failed to open backup db file: %w", err)
	}
	dbBackupFileStat, err := dbBackupFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat db backup file: %w", err)
	}
	dbSizeGauge.Set(float64(dbBackupFileStat.Size()))
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: r.context.Config.S3BackupUrl,
		}, nil
	})
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithEndpointResolverWithOptions(r2Resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(r.context.Config.S3BackupAccessKeyId, r.context.Config.S3BackupSecretAccessKey, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		return fmt.Errorf("failed to load r2 config")
	}

	client := s3.NewFromConfig(cfg)

	bucket := r.context.Config.S3BackupBucket
	key := "rasende2/db-backup.db"

	objects, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucket,
	})
	if err != nil {
		return fmt.Errorf("failed to list r2 objects: %w", err)
	}
	if len(objects.Contents) > 0 {
		existingObject := objects.Contents[0]
		objFound := false
		for _, obj := range objects.Contents {
			if obj.Key != nil && *obj.Key == key {
				existingObject = obj
				objFound = true
				break
			}
		}
		// do not attempt to over-write a larger file with a smaller file
		if objFound && existingObject.Size != nil && *existingObject.Size > dbBackupFileStat.Size() {
			return fmt.Errorf("attemping to over-write large file (%v) in r2, with small local file (%v)", *existingObject.Size, dbBackupFileStat.Size())
		}
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   dbBackupFile,
	})
	if err != nil {
		return fmt.Errorf("failed to upload db backup file: %w", err)
	}

	err = dbBackupFile.Close()
	if err != nil {
		return fmt.Errorf("failed to close db backup file: %w", err)
	}

	err = os.Remove(r.context.Config.BackupDbPath)
	if err != nil {
		return fmt.Errorf("failed to remove local db backup file: %w", err)
	}

	return nil
}

func (r *RssService) AddMissingItemsToSearchIndexAndLogError(ctx context.Context, maxLookBack *time.Time) {
	err := r.addMissingItemsToSearchIndex(ctx, maxLookBack)
	if err != nil {
		log.Printf("failed to add missing items to search index: %v", err)
	}
}

func (r *RssService) addMissingItemsToSearchIndex(ctx context.Context, maxLookBack *time.Time) error {
	rssUrls, err := r.repository.GetRssUrls()
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

func (r *RssService) addMissingItemsToSearchIndexForSite(ctx context.Context, rssUrl RssUrlDto, maxLookBack *time.Time) error {
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
			err = r.indexItemIds(rssItemIdsToIndex, rssUrl)
			if err != nil {
				return fmt.Errorf("error indexing item ids: %w", err)
			}
		}
	}
	return nil
}

func (r *RssService) indexItemIds(allItemIds []string, rssUrl RssUrlDto) error {
	if len(allItemIds) == 0 {
		return nil
	}
	chunkSize := 3000
	if rssUrl.ArticleHasContent {
		chunkSize = 100
	}
	itemIdChunks := lo.Chunk(allItemIds, chunkSize)
	for _, itemIds := range itemIdChunks {
		items, err := r.repository.GetItemsByIds(itemIds)
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
