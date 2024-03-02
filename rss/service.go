package rss

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
)

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

func (r *RssService) GetRecentItems(ctx context.Context, siteName string, offset int, limit int) ([]RssItemDto, error) {
	items, err := r.repository.GetRecentItems(ctx, siteName, offset, limit)
	return items, err
}

func (r *RssService) SearchItems(ctx context.Context, query string, searchContent bool, offset int, limit int, after *time.Time, orderBy string) ([]RssItemDto, error) {
	var items []RssItemDto = []RssItemDto{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	searchResult, err := r.search.Search(ctx, query, limit, offset, after, orderBy, searchContent)
	if err != nil {
		return items, fmt.Errorf("failed to search: %w", err)
	}
	itemIds := make([]string, len(searchResult.Hits))
	for i, doc := range searchResult.Hits {
		itemIds[i] = doc.ID
	}
	items, err = r.repository.GetItemsByIds(itemIds, after, orderBy)
	return items, err
}

func (r *RssService) fetchAndSaveNewItemsForSite(rssUrl RssUrlDto) error {
	fromFeed, err := r.parse(rssUrl)
	if err != nil {
		return fmt.Errorf("failed to get items from feed %v: %w", rssUrl.Name, err)
	}

	fromFeedItemIds := make([]string, len(fromFeed))
	for i, fromFeedItem := range fromFeed {
		fromFeedItemIds[i] = fromFeedItem.ItemId
	}

	existing, err := r.repository.GetItemsByNameAndIds(rssUrl.Name, fromFeedItemIds)
	if err != nil {
		return fmt.Errorf("failed to get items for %v: %w", rssUrl.Name, err)
	}
	existingIds := make(map[string]bool)
	for _, item := range existing {
		existingIds[item.ItemId] = true
	}
	toInsert := make([]RssItemDto, 0)
	for _, item := range fromFeed {
		_, exists := existingIds[item.ItemId]
		if !exists {
			toInsert = append(toInsert, item)
		}
	}

	log.Printf("FetchAndSaveNewItems: %v inserted %v new items", rssUrl.Name, len(toInsert))
	err = r.repository.InsertItems(toInsert)
	if err != nil {
		return fmt.Errorf("failed to insert items for %v: %w", rssUrl.Name, err)
	}
	err = r.search.Index(toInsert)
	if err != nil {
		log.Printf("failed to index items: %v", err)
	}
	totalCount, err := r.repository.GetItemCount(rssUrl.Name)
	if err != nil {
		log.Printf("failed to get item count: %v", err)
	} else {
		rssArticleCount.WithLabelValues(rssUrl.Name).Set(float64(totalCount))
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

func (r *RssService) GetFakeNews(siteName string, title string) (*FakeNewsDto, error) {
	return r.repository.GetFakeNews(siteName, title)
}

func (r *RssService) CreateFakeNews(siteName string, title string) error {
	return r.repository.CreateFakeNews(siteName, title)
}
func (r *RssService) UpdateFakeNews(siteName string, title string, content string) error {
	return r.repository.UpdateFakeNews(siteName, title, content)
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
