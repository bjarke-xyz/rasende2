package rss

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/lang/da"
	"github.com/blevesearch/bleve/v2/search/query"
)

type RssSearch struct {
	indexPath string
}

func NewRssSearch(indexPath string) *RssSearch {
	return &RssSearch{
		indexPath: indexPath,
	}
}

func (s *RssSearch) CreateIndexIfNotExists() error {
	if _, err := os.Stat(s.indexPath); !os.IsNotExist(err) {
		// index already exists, do nothing
		return nil
	}
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultAnalyzer = da.AnalyzerName
	index, err := bleve.New(s.indexPath, indexMapping)
	if err != nil {
		return err
	}
	err = index.Close()
	if err != nil {
		return err
	}
	return nil
}

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
	return nil
}

func (s *RssSearch) Search(ctx context.Context, searchQuery string, size int, from int, after *time.Time, orderBy string, searchContent bool) (*bleve.SearchResult, error) {
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
		disjunctuinQuery := query.NewDisjunctionQuery([]query.Query{titleQuery, contentQuery})
		disjunctuinQuery.Min = 1 // match at least one, either title or content
		bleveQuery = disjunctuinQuery
	}
	searchReq := bleve.NewSearchRequestOptions(bleveQuery, size, from, false)
	searchReq.SortBy([]string{orderBy})
	searchResult, err := index.Search(searchReq)
	if err != nil {
		return nil, err
	}
	return searchResult, nil
}
