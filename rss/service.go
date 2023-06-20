package rss

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

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

func NewRssService(context *pkg.AppContext, repository *RssRepository) *RssService {
	return &RssService{
		context:    context,
		repository: repository,
		sanitizer:  bluemonday.StrictPolicy(),
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

func (r *RssService) SearchItems(ctx context.Context, query string, searchContent bool, offset int, limit int, after *time.Time) ([]RssItemDto, error) {
	var items []RssItemDto = []RssItemDto{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	cacheKey := fmt.Sprintf("SearchItems:%v:%v:%v:%v:%v", query, searchContent, offset, limit, after)
	if err := r.context.Cache.Get(ctx, cacheKey, &items); err == nil {
		return items, nil
	}
	items, err := r.repository.SearchItems(query, searchContent, offset, limit, after)
	if err == nil {
		r.context.Cache.Set(ctx, cacheKey, items, time.Hour)
	}
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

	existing, err := r.repository.GetItemsByIds(rssUrl.Name, fromFeedItemIds)
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
