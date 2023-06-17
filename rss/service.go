package rss

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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
		"status_code", "name",
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

func (r *RssService) SearchItems(ctx context.Context, query string, searchContent bool) ([]RssItemDto, error) {
	var items []RssItemDto = []RssItemDto{}
	if len(query) > 50 || len(query) <= 2 {
		return items, nil
	}
	cacheKey := fmt.Sprintf("SearchItems:%v:%v", query, searchContent)
	if err := r.context.Cache.Get(ctx, cacheKey, &items); err == nil {
		return items, nil
	}
	items, err := r.repository.SearchItems(query, searchContent)
	if err == nil {
		r.context.Cache.Set(ctx, cacheKey, items, time.Hour)
	}
	return items, err
}

func (r *RssService) FetchAndSaveNewItems() error {
	rssUrls, err := r.repository.GetRssUrls()
	if err != nil {
		return fmt.Errorf("failed to get rss urls: %w", err)
	}
	errors := make([]error, 0)
	for _, rssUrl := range rssUrls {
		toInsert := make([]RssItemDto, 0)
		existing, err := r.repository.GetItems(rssUrl.Name)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to get items for %v: %w", rssUrl.Name, err))
			continue
		}
		existingIds := make(map[string]bool)
		for _, item := range existing {
			existingIds[item.ItemId] = true
		}

		fromFeed, err := r.parse(rssUrl)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to get items from feed %v: %w", rssUrl.Name, err))
			continue
		}
		for _, item := range fromFeed {
			_, exists := existingIds[item.ItemId]
			if !exists {
				toInsert = append(toInsert, item)
			}
		}

		log.Printf("FetchAndSaveNewItems: %v inserted %v new items", rssUrl.Name, len(toInsert))
		err = r.repository.InsertItems(toInsert)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to insert items for %v: %w", rssUrl.Name, err))
			continue
		}
		totalCount := len(existing) + len(toInsert)
		rssArticleCount.WithLabelValues(rssUrl.Name).Set(float64(totalCount))
	}
	if len(errors) > 0 {
		err := errors[0]
		for i, e := range errors {
			if i == 0 {
				continue
			}
			err = fmt.Errorf("%v. %w", err, e)
		}
		return err
	}
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
		rssFetchStatusCodes.WithLabelValues(fmt.Sprintf("%v", resp.StatusCode), rssUrl.Name).Inc()
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
