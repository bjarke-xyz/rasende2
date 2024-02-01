package rss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/samber/lo"
	openai "github.com/sashabaranov/go-openai"
)

type HttpHandlers struct {
	context      *pkg.AppContext
	service      *RssService
	openaiClient *ai.OpenAIClient
	search       *RssSearch
}

func NewHttpHandlers(context *pkg.AppContext, service *RssService, openaiClient *ai.OpenAIClient, search *RssSearch) *HttpHandlers {
	return &HttpHandlers{
		context:      context,
		service:      service,
		openaiClient: openaiClient,
		search:       search,
	}
}

type SearchResult struct {
	HighlightedWords []string     `json:"highlightedWords"`
	Items            []RssItemDto `json:"items"`
}

func intQuery(c *gin.Context, query string, defaultVal int) int {
	valStr := c.DefaultQuery(query, fmt.Sprintf("%v", defaultVal))
	val, err := strconv.Atoi(valStr)
	if err != nil {
		val = defaultVal
	}
	return val
}

func float32Query(c *gin.Context, query string, defaultVal float32) float32 {
	valStr := c.DefaultQuery(query, fmt.Sprintf("%v", defaultVal))
	val, err := strconv.ParseFloat(valStr, 32)
	if err != nil {
		val = float64(defaultVal)
	}
	return float32(val)
}

func returnError(c *gin.Context, err error) {
	log.Printf("error: %v", err)
	c.Status(500)
}

func (h *HttpHandlers) HandleMigrate(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != key {
			c.AbortWithStatus(401)
			return
		}
		pgDb, err := sqlx.Open("postgres", os.Getenv("POSTGRES_DB_CONN_STR"))
		if err != nil {
			returnError(c, fmt.Errorf("failed to open: %w", err))
			return
		}
		var pgRssItems []RssItemDto
		err = pgDb.Select(&pgRssItems, "SELECT item_id, site_name, title, content, link, published FROM rss_items")
		if err != nil {
			returnError(c, err)
			return
		}
		chunks := lo.Chunk(pgRssItems, 5000)
		for i, chunk := range chunks {
			log.Printf("chunk %v of %v", i, len(chunks))
			err = h.service.repository.InsertItems(chunk)
			if err != nil {
				returnError(c, err)
				return
			}
		}
		h.search.Index(pgRssItems)
		c.String(200, fmt.Sprintf("%v", len(pgRssItems)))
	}
}

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (h *HttpHandlers) HandleSearch(c *gin.Context) {
	query := c.Query("q")
	offset := intQuery(c, "offset", 0)
	limit := intQuery(c, "limit", 10)
	if limit > 100 {
		limit = 10
	}
	searchContentStr := c.DefaultQuery("content", "false")
	searchContent, err := strconv.ParseBool(searchContentStr)
	orderBy := c.DefaultQuery("orderBy", allowedOrderBys[0])
	if !lo.Contains(allowedOrderBys, orderBy) {
		orderBy = allowedOrderBys[0]
	}
	if err != nil {
		searchContent = false
	}
	results, err := h.service.SearchItems(c.Request.Context(), query, searchContent, offset, limit, nil, orderBy)
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		c.JSON(http.StatusInternalServerError, SearchResult{})
		return
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	c.JSON(http.StatusOK, SearchResult{
		HighlightedWords: []string{query},
		Items:            results,
	})
}

type ChartDataset struct {
	Label string `json:"label"`
	Data  []int  `json:"data"`
}

type ChartResult struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Labels   []string       `json:"labels"`
	Datasets []ChartDataset `json:"datasets"`
}

type ChartsResult struct {
	Charts []ChartResult `json:"charts"`
}

func MakeLineChart(items []RssItemDto, title string, datasetLabel string) ChartResult {
	dateFormat := "01-02"
	today := time.Now()
	sevenDaysAgo := today.Add(-time.Hour * 24 * 6)
	lastWeekItemsGroupedByDate := make(map[string]int)
	for _, item := range items {
		if item.Published.Before(today) && item.Published.After(sevenDaysAgo) {
			key := item.Published.Format(dateFormat)
			_, ok := lastWeekItemsGroupedByDate[key]
			if !ok {
				lastWeekItemsGroupedByDate[key] = 0
			}
			lastWeekItemsGroupedByDate[key] = lastWeekItemsGroupedByDate[key] + 1
		}
	}
	labels := make([]string, 0)
	data := make([]int, 0)
	for d := sevenDaysAgo; !d.After(today); d = d.AddDate(0, 0, 1) {
		labels = append(labels, d.Format(dateFormat))
		datum, ok := lastWeekItemsGroupedByDate[d.Format(dateFormat)]
		if ok {
			data = append(data, datum)
		} else {
			data = append(data, 0)
		}
	}

	return ChartResult{
		Type:   "line",
		Title:  title,
		Labels: labels,
		Datasets: []ChartDataset{
			{
				Label: datasetLabel,
				Data:  data,
			},
		},
	}
}

func MakeDoughnutChart(items []RssItemDto, title string) ChartResult {
	sitesSet := make(map[string][]RssItemDto)
	for _, item := range items {
		_, ok := sitesSet[item.SiteName]
		if !ok {
			sitesSet[item.SiteName] = make([]RssItemDto, 0)
		}
		sitesSet[item.SiteName] = append(sitesSet[item.SiteName], item)

	}

	labels := make([]string, 0)
	data := make([]int, 0)
	for siteName := range sitesSet {
		labels = append(labels, siteName)
	}
	sort.Strings(labels)
	for _, siteName := range labels {
		siteItems, ok := sitesSet[siteName]
		if ok {
			data = append(data, len(siteItems))
		}
	}

	return ChartResult{
		Type:   "pie",
		Title:  title,
		Labels: labels,
		Datasets: []ChartDataset{
			{
				Label: "",
				Data:  data,
			},
		},
	}
}

func (h *HttpHandlers) HandleCharts(c *gin.Context) {
	query := c.Query("q")
	sevenDaysAgo := time.Now().Add(-time.Hour * 24 * 6)
	results, err := h.service.SearchItems(c.Request.Context(), query, false, 0, 100000, &sevenDaysAgo, "published")
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	lineTitle := "Den seneste uges raserier"
	lineDatasetLabel := "Raseriudbrud"
	doughnutTitle := "Raseri i de forskellige medier"
	if query != "rasende" {
		lineTitle = "Den seneste uges brug af '" + query + "'"
		lineDatasetLabel = "Antal '" + query + "'"
		doughnutTitle = "Brug af '" + query + "' i de forskellige medier"
	}
	c.JSON(http.StatusOK, ChartsResult{
		Charts: []ChartResult{
			MakeLineChart(results, lineTitle, lineDatasetLabel),
			MakeDoughnutChart(results, doughnutTitle),
		},
	})
}

func (h *HttpHandlers) RunJob(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != key {
			c.AbortWithStatus(401)
			return
		}
		fireAndForget := c.Query("fireAndForget") == "true"
		if fireAndForget {
			go h.context.JobManager.RunJob(JobIdentifierIngestion)
		} else {
			h.context.JobManager.RunJob(JobIdentifierIngestion)
		}
		c.Status(http.StatusOK)
	}
}

func (h *HttpHandlers) BackupDb(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != key {
			c.AbortWithStatus(401)
			return
		}
		fireAndForget := c.Query("fireAndForget") == "true"
		if fireAndForget {
			go func() {
				err := h.service.BackupDb(context.Background())
				if err != nil {
					log.Printf("backup failed: %v", err)
				} else {
					log.Printf("backup success")
				}
			}()
		} else {
			ctx := c.Request.Context()
			err := h.service.BackupDb(ctx)
			if err != nil {
				log.Printf("backup failed: %v", err)
				c.String(http.StatusInternalServerError, "backup failed: %v", err)
				return
			}
			log.Printf("backup success")
		}
		c.Status(http.StatusOK)
	}
}

func (h *HttpHandlers) HandleSites(c *gin.Context) {
	siteNames, err := h.service.GetSiteNames()
	if err != nil {
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	c.JSON(http.StatusOK, siteNames)
}

type ContentEvent struct {
	Content string
}

func (h *HttpHandlers) HandleGenerateTitles(c *gin.Context) {
	siteName := c.Query("siteName")
	if siteName == "" {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	offset := intQuery(c, "offset", 0)
	defaultLimit := 300
	limit := intQuery(c, "limit", defaultLimit)
	if limit > defaultLimit {
		limit = defaultLimit
	}
	temperature := float32Query(c, "temperature", 0.5)
	if temperature > 1 {
		temperature = 1
	}
	if temperature < 0 {
		temperature = 0
	}
	rssUrls, err := h.service.GetRssUrls()
	if err != nil {
		log.Printf("get rss urls failed: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
	}
	rssUrl := RssUrlDto{}
	for _, r := range rssUrls {
		if r.Name == siteName {
			rssUrl = r
			break
		}
	}

	items, err := h.service.repository.GetRecentItems(c.Request.Context(), siteName, offset, limit)
	if err != nil {
		log.Printf("get items failed: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusNotFound, nil)
		return
	}
	itemTitles := make([]string, len(items))
	for i, item := range items {
		itemTitles[i] = item.Title
	}
	rand.Shuffle(len(itemTitles), func(i, j int) { itemTitles[i], itemTitles[j] = itemTitles[j], itemTitles[i] })
	stream, err := h.openaiClient.GenerateArticleTitles(c.Request.Context(), siteName, rssUrl.Description, itemTitles, 10, temperature)
	if err != nil {
		log.Printf("openai failed: %v", err)

		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			c.JSON(http.StatusTooManyRequests, nil)
		} else {
			c.JSON(http.StatusInternalServerError, nil)
		}
		return
	}

	var sb strings.Builder
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(w io.Writer) bool {
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				titlesStr := sb.String()
				titles := strings.Split(titlesStr, "\n")
				for _, title := range titles {
					title := strings.TrimSpace(title)
					if len(title) > 0 {
						err = h.service.CreateFakeNews(siteName, title)
						if err != nil {
							log.Printf("create fake news failed for site %v, title %v: %v", siteName, title, err)
						}
					}
				}
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				return false
			}
			sb.WriteString(response.Choices[0].Delta.Content)
			contentEvent := ContentEvent{
				Content: response.Choices[0].Delta.Content,
			}
			c.SSEvent("message", contentEvent)
			c.Writer.Flush()
		}
	})
}

func (h *HttpHandlers) HandleGenerateArticleContent(c *gin.Context) {

	siteName := c.Query("siteName")
	if siteName == "" {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	articleTitle := c.Query("title")
	if articleTitle == "" {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	articleTitle = strings.TrimSpace(articleTitle)
	siteNames, err := h.service.GetSiteNames()
	if err != nil {
		log.Printf("error getting site names: %v", err)
	}
	siteFound := false
	for _, site := range siteNames {
		if site == siteName {
			siteFound = true
			break
		}
	}
	if !siteFound {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	existing, err := h.service.GetFakeNews(siteName, articleTitle)
	if err != nil {
		log.Printf("error getting existing news: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	if existing == nil {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	if len(existing.Content) > 0 {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		c.Stream(func(w io.Writer) bool {
			chunks := Chunks(existing.Content, 10)
			for _, chunk := range chunks {
				contentEvent := ContentEvent{
					Content: chunk,
				}
				c.SSEvent("message", contentEvent)
				c.Writer.Flush()
				time.Sleep(25 * time.Millisecond)
			}
			return false
		})
		return
	}

	var temperature float32 = 1.0
	rssUrls, err := h.service.GetRssUrls()
	if err != nil {
		log.Printf("get rss urls failed: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
	}
	rssUrl := RssUrlDto{}
	for _, r := range rssUrls {
		if r.Name == siteName {
			rssUrl = r
			break
		}
	}
	stream, err := h.openaiClient.GenerateArticleContent(c.Request.Context(), siteName, rssUrl.Description, articleTitle, temperature)
	if err != nil {
		log.Printf("openai failed: %v", err)

		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			c.JSON(http.StatusTooManyRequests, nil)
		} else {
			c.JSON(http.StatusInternalServerError, nil)
		}
		return
	}

	var sb strings.Builder
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(w io.Writer) bool {
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				articleContent := sb.String()
				err = h.service.UpdateFakeNews(siteName, articleTitle, articleContent)
				if err != nil {
					log.Printf("error saving fake news: %v", err)
				}
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				return false
			}
			sb.WriteString(response.Choices[0].Delta.Content)
			contentEvent := ContentEvent{
				Content: response.Choices[0].Delta.Content,
			}
			c.SSEvent("message", contentEvent)
			c.Writer.Flush()
		}
	})
}

func Chunks(s string, chunkSize int) []string {
	if len(s) == 0 {
		return nil
	}
	if chunkSize >= len(s) {
		return []string{s}
	}
	var chunks []string = make([]string, 0, (len(s)-1)/chunkSize+1)
	currentLen := 0
	currentStart := 0
	for i := range s {
		if currentLen == chunkSize {
			chunks = append(chunks, s[currentStart:i])
			currentLen = 0
			currentStart = i
		}
		currentLen++
	}
	chunks = append(chunks, s[currentStart:])
	return chunks
}
