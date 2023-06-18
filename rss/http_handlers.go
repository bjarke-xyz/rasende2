package rss

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/gin-gonic/gin"
)

type HttpHandlers struct {
	context      *pkg.AppContext
	service      *RssService
	openaiClient *ai.OpenAIClient
}

func NewHttpHandlers(context *pkg.AppContext, service *RssService, openaiClient *ai.OpenAIClient) *HttpHandlers {
	return &HttpHandlers{
		context:      context,
		service:      service,
		openaiClient: openaiClient,
	}
}

type SearchResult struct {
	HighlightedWords []string     `json:"highlightedWords"`
	Items            []RssItemDto `json:"items"`
}

var defaultLimit = 10
var defaultOffset = 0

func (h *HttpHandlers) HandleSearch(c *gin.Context) {
	query := c.Query("q")
	offsetStr := c.DefaultQuery("offset", fmt.Sprintf("%v", defaultOffset))
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		offset = defaultOffset
	}
	limitStr := c.DefaultQuery("limit", fmt.Sprintf("%v", defaultLimit))
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = defaultLimit
	}
	if limit > 100 {
		limit = defaultLimit
	}
	searchContentStr := c.DefaultQuery("content", "false")
	searchContent, err := strconv.ParseBool(searchContentStr)
	if err != nil {
		searchContent = false
	}
	results, err := h.service.SearchItems(c.Request.Context(), query, searchContent, offset, limit, nil)
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
	results, err := h.service.SearchItems(c.Request.Context(), query, false, 0, 100000, &sevenDaysAgo)
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
	items, err := h.service.repository.GetRecentItems(c.Request.Context(), siteName, 0, 200)
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
	stream, err := h.openaiClient.GenerateArticleTitles(c.Request.Context(), siteName, itemTitles, 10)
	if err != nil {
		log.Printf("openai failed: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(w io.Writer) bool {
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				return false
			}
			contentEvent := ContentEvent{
				Content: response.Choices[0].Delta.Content,
			}
			c.SSEvent("message", contentEvent)
		}
	})
}
