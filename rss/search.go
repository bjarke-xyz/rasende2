package rss

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/da"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type RssSearch struct {
	indexPath string
}

func NewRssSearch(indexPath string) *RssSearch {
	return &RssSearch{
		indexPath: indexPath,
	}
}

func (s *RssSearch) CreateIndexIfNotExists() (bool, error) {
	if _, err := os.Stat(s.indexPath); !os.IsNotExist(err) {
		// index already exists, do nothing
		return false, nil
	}
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultAnalyzer = da.AnalyzerName
	index, err := bleve.New(s.indexPath, indexMapping)
	if err != nil {
		return false, err
	}
	err = index.Close()
	if err != nil {
		return false, err
	}
	return true, nil
}

var indexSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "rasende2_index_size_bytes",
	Help: "Size in bytes of rasende2 search index",
})

func (s *RssSearch) Index(items []RssItemDto) error {
	index, err := bleve.Open(s.indexPath)
	if err != nil {
		return err
	}
	defer index.Close()
	batchSize := 5000
	batchCount := 0
	count := 0
	startTime := time.Now()
	log.Printf("Indexing...")
	batch := index.NewBatch()
	for _, item := range items {
		batch.Index(item.ItemId, item)
		batchCount++
		if batchCount >= batchSize {
			err = index.Batch(batch)
			if err != nil {
				return err
			}
			batch = index.NewBatch()
			batchCount = 0
		}

		count++
		if count%1000 == 0 {
			indexDuration := time.Since(startTime)
			indexDurationSeconds := float64(indexDuration) / float64(time.Second)
			timePerDoc := float64(indexDuration) / float64(count)
			log.Printf("Indexed %d documents, in %.2fs (average %.2fms/doc)", count, indexDurationSeconds, timePerDoc/float64(time.Millisecond))
		}
	}
	// flush the last batch
	if batchCount > 0 {
		err = index.Batch(batch)
		if err != nil {
			return err
		}
	}
	indexDuration := time.Since(startTime)
	indexDurationSeconds := float64(indexDuration) / float64(time.Second)
	timePerDoc := float64(indexDuration) / float64(count)
	log.Printf("Indexed %d documents, in %.2fs (average %.2fms/doc)", count, indexDurationSeconds, timePerDoc/float64(time.Millisecond))
	statsMap := index.StatsMap()
	totalSize := getTotalSize(statsMap)
	indexSizeGauge.Set(float64(totalSize))
	return nil
}

func getTotalSize(statsMap map[string]interface{}) int64 {
	totalSize := int64(0)
	if val, ok := statsMap["CurOnDiskBytes"]; ok {
		totalSize += int64(val.(float64))
	}
	return totalSize
}

func (s *RssSearch) HasItem(ctx context.Context, itemId string) (bool, error) {
	index, err := bleve.Open(s.indexPath)
	if err != nil {
		return false, fmt.Errorf("error opening index: %w", err)
	}
	defer index.Close()
	doc, err := index.Document(itemId)
	if err != nil {
		return false, fmt.Errorf("error getting document: %w", err)
	}
	return doc != nil, nil
}

func (s *RssSearch) HasItems(ctx context.Context, itemIds []string) (map[string]any, error) {
	result := make(map[string]any, 0)
	if len(itemIds) == 0 {
		return result, nil
	}
	index, err := bleve.Open(s.indexPath)
	if err != nil {
		return result, fmt.Errorf("error opening index: %w", err)
	}
	defer index.Close()
	for _, itemId := range itemIds {
		doc, err := index.Document(itemId)
		if err != nil {
			return result, fmt.Errorf("error getting document, id=%v: %w", itemId, err)
		}
		if doc != nil {
			result[itemId] = struct{}{}
		}
	}
	return result, nil
}

func (s *RssSearch) Search(ctx context.Context, searchQuery string, size int, from int, start *time.Time, end *time.Time, orderBy string, searchContent bool, returnFields []string) (*bleve.SearchResult, error) {
	index, err := bleve.Open(s.indexPath)
	if err != nil {
		return nil, err
	}
	defer index.Close()
	// bleveQuery := bleve.NewQueryStringQuery(query)
	titleQuery := bleve.NewMatchQuery(searchQuery)
	titleQuery.SetField("title")
	var bleveQuery query.Query = titleQuery
	if searchContent {
		contentQuery := bleve.NewMatchQuery(searchQuery)
		contentQuery.SetField("content")
		disjunctionQuery := query.NewDisjunctionQuery([]query.Query{titleQuery, contentQuery})
		disjunctionQuery.Min = 1 // match at least one, either title or content
		bleveQuery = disjunctionQuery
	}
	if start != nil || end != nil {
		bleveStart := time.Time{}
		if start != nil {
			bleveStart = *start
		}
		bleveEnd := time.Time{}
		if end != nil {
			bleveEnd = *end
		}
		log.Printf("bleve search, start=%v end=%v", bleveStart, bleveEnd)
		dateRangeQuery := bleve.NewDateRangeQuery(bleveStart, bleveEnd)
		dateRangeQuery.SetField("published")
		conjunctionQuery := query.NewConjunctionQuery([]query.Query{bleveQuery, dateRangeQuery})
		bleveQuery = conjunctionQuery
	}
	searchReq := bleve.NewSearchRequestOptions(bleveQuery, size, from, false)
	searchReq.SortBy([]string{orderBy})
	if returnFields != nil {
		searchReq.Fields = returnFields
	}
	searchResult, err := index.Search(searchReq)
	if err != nil {
		return nil, err
	}
	return searchResult, nil
}
