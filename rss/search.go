package rss

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/da"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type RssSearch struct {
	indexPath string
	index     bleve.Index
}

func NewRssSearch(indexPath string) *RssSearch {
	return &RssSearch{
		indexPath: indexPath,
	}
}

func buildIndexMapping() *mapping.IndexMappingImpl {
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultAnalyzer = da.AnalyzerName
	rssItemMapping := bleve.NewDocumentMapping()

	titleFieldMapping := bleve.NewTextFieldMapping()
	titleFieldMapping.Analyzer = da.AnalyzerName
	rssItemMapping.AddFieldMappingsAt("title", titleFieldMapping)

	contentFieldMapping := bleve.NewTextFieldMapping()
	contentFieldMapping.Analyzer = da.AnalyzerName
	rssItemMapping.AddFieldMappingsAt("content", contentFieldMapping)

	publishedFieldMapping := bleve.NewDateTimeFieldMapping()
	rssItemMapping.AddFieldMappingsAt("published", publishedFieldMapping)

	// Using text field mapping here as it is "lighter" compared to numeric, and we don't need to do numeric operations on the id (TODO: find GitHub issue comment that said this)
	siteIdFieldMapping := bleve.NewTextFieldMapping()
	rssItemMapping.AddFieldMappingsAt("siteId", siteIdFieldMapping)

	linkFieldMapping := bleve.NewTextFieldMapping()
	linkFieldMapping.Index = false
	linkFieldMapping.Store = true
	rssItemMapping.AddFieldMappingsAt("link", linkFieldMapping)
	return indexMapping
}

func (s *RssSearch) createIndex() (bleve.Index, error) {
	indexMapping := buildIndexMapping()

	index, err := bleve.NewUsing(s.indexPath, indexMapping, "scorch", "scorch", nil)
	if err != nil {
		return nil, fmt.Errorf("error creating index: %w", err)
	}
	return index, nil
}

func (s *RssSearch) CloseIndex() error {
	if s.index != nil {
		err := s.index.Close()
		if err != nil {
			log.Printf("error closing index: %v", err)
			return fmt.Errorf("error closing index: %w", err)
		}
	}
	return nil
}

func (s *RssSearch) OpenAndCreateIndexIfNotExists() (bool, error) {
	indexCreated := false
	index, err := bleve.Open(s.indexPath)
	if err != nil {
		if err == bleve.ErrorIndexPathDoesNotExist {
			index, err = s.createIndex()
			if err != nil {
				return indexCreated, err
			}
			indexCreated = true
		} else {
			return indexCreated, fmt.Errorf("error opening index at %s: %w", s.indexPath, err)
		}
	}
	s.index = index
	return indexCreated, nil
}

var indexSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "rasende2_index_size_bytes",
	Help: "Size in bytes of rasende2 search index",
})

func (s *RssSearch) Index(items []RssItemDto) error {
	batchSize := 5000
	batchCount := 0
	count := 0
	startTime := time.Now()
	log.Printf("Indexing...")
	batch := s.index.NewBatch()
	for _, item := range items {
		batch.Index(item.ItemId, item)
		batchCount++
		if batchCount >= batchSize {
			err := s.index.Batch(batch)
			if err != nil {
				return err
			}
			batch = s.index.NewBatch()
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
		err := s.index.Batch(batch)
		if err != nil {
			return err
		}
	}
	indexDuration := time.Since(startTime)
	indexDurationSeconds := float64(indexDuration) / float64(time.Second)
	timePerDoc := float64(indexDuration) / float64(count)
	log.Printf("Indexed %d documents, in %.2fs (average %.2fms/doc)", count, indexDurationSeconds, timePerDoc/float64(time.Millisecond))
	statsMap := s.index.StatsMap()
	totalSize := getTotalSize(statsMap)
	indexSizeGauge.Set(float64(totalSize))
	return nil
}

func getTotalSize(statsMap map[string]interface{}) uint64 {
	totalSize := uint64(0)
	indexMap, ok := statsMap["index"].(map[string]interface{})
	if ok {
		if val, ok := indexMap["CurOnDiskBytes"]; ok {
			totalSize += val.(uint64)
		}
	}
	return totalSize
}

func (s *RssSearch) RefreshMetrics() {
	if s.index == nil {
		return
	}
	statsMap := s.index.StatsMap()
	size := getTotalSize(statsMap)
	indexSizeGauge.Set(float64(size))
}

func (s *RssSearch) HasItem(ctx context.Context, itemId string) (bool, error) {
	doc, err := s.index.Document(itemId)
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
	for _, itemId := range itemIds {
		doc, err := s.index.Document(itemId)
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
	searchResult, err := s.index.Search(searchReq)
	if err != nil {
		return nil, err
	}
	return searchResult, nil
}
