package rss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/ginutils"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/samber/lo"
	openai "github.com/sashabaranov/go-openai"
)

const PlaceholderImgUrl = "https://static.bjarke.xyz/placeholder.png"

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
	HighlightedWords []string          `json:"highlightedWords"`
	Items            []RssSearchResult `json:"items"`
}

// func returnError(c *gin.Context, err error) {
// 	log.Printf("error: %v", err)
// 	c.Status(500)
// }

// func (h *HttpHandlers) HandleMigrate(key string) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		if c.GetHeader("Authorization") != key {
// 			c.AbortWithStatus(401)
// 			return
// 		}
// 		sqliteDb, err := sqlx.Open("sqlite3", os.Getenv("DB_CONN_STR"))
// 		if err != nil {
// 			returnError(c, fmt.Errorf("failed to open: %w", err))
// 			return
// 		}
// 		var pgRssItems []RssItemDto
// 		err = sqliteDb.Select(&pgRssItems, "SELECT item_id, site_name, title, content, link, published FROM rss_items")
// 		if err != nil {
// 			returnError(c, err)
// 			return
// 		}
// 		chunks := lo.Chunk(pgRssItems, 5000)
// 		for i, chunk := range chunks {
// 			log.Printf("chunk %v of %v", i, len(chunks))
// 			err = h.service.repository.InsertItems(chunk)
// 			if err != nil {
// 				returnError(c, err)
// 				return
// 			}
// 		}
// 		h.search.Index(pgRssItems)
// 		c.String(200, fmt.Sprintf("%v", len(pgRssItems)))
// 	}
// }

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (h *HttpHandlers) HandleSearch(c *gin.Context) {
	query := c.Query("q")
	offset := ginutils.IntQuery(c, "offset", 0)
	limit := ginutils.IntQuery(c, "limit", 10)
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
	results, err := h.service.SearchItems(c.Request.Context(), query, searchContent, offset, limit, orderBy)
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

func MakeLineChartFromSearchQueryCount(searchQueryCounts []SearchQueryCount, title string, datasetLabel string) ChartResult {
	dateFormat := "01-02"
	labels := make([]string, 7)
	data := make([]int, 7)
	weekItemsGroupedByDate := make(map[int64]int, 0)
	for _, v := range searchQueryCounts {
		weekItemsGroupedByDate[v.Timestamp.Unix()] = v.Count
	}

	today := time.Now()
	sevenDaysAgo := today.Add(-time.Hour * 24 * 6)
	i := 0
	for d := sevenDaysAgo; !d.After(today); d = d.AddDate(0, 0, 1) {
		dKey := d.Truncate(time.Hour * 24)
		labels[i] = dKey.Format(dateFormat)
		datum, ok := weekItemsGroupedByDate[dKey.Unix()]
		if ok {
			data[i] = datum
		}
		i++
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

func MakeDoughnutChartFromSiteCount(siteCounts []SiteCount, title string) ChartResult {

	labels := make([]string, len(siteCounts))
	data := make([]int, len(siteCounts))
	for i, siteCount := range siteCounts {
		labels[i] = siteCount.SiteName
		data[i] = siteCount.Count
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
	ctx := c.Request.Context()
	query := c.Query("q")
	isRasende := query == "rasende"

	siteCountPromise := pkg.NewPromise(func() ([]SiteCount, error) {
		return h.service.GetSiteCountForSearchQuery(ctx, query, false)
	})

	now := time.Now()
	sevenDaysAgo := now.Add(-time.Hour * 24 * 6)
	tomorrow := now.Add(time.Hour * 24)
	itemCount, err := h.service.GetItemCountForSearchQuery(ctx, query, false, &sevenDaysAgo, &tomorrow, "published")
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}

	siteCount, err := siteCountPromise.Get()
	if err != nil {
		log.Printf("failed to get site count with query %v: %v", query, err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}

	lineTitle := "Den seneste uges raserier"
	lineDatasetLabel := "Raseriudbrud"
	doughnutTitle := "Raseri i de forskellige medier"
	if !isRasende {
		lineTitle = "Den seneste uges brug af '" + query + "'"
		lineDatasetLabel = "Antal '" + query + "'"
		doughnutTitle = "Brug af '" + query + "' i de forskellige medier"
	}
	chartsResult := ChartsResult{
		Charts: []ChartResult{
			MakeLineChartFromSearchQueryCount(itemCount, lineTitle, lineDatasetLabel),
			MakeDoughnutChartFromSiteCount(siteCount, doughnutTitle),
		},
	}
	c.JSON(http.StatusOK, chartsResult)
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
			go h.service.BackupDbAndLogError(context.Background())
		} else {
			ctx := c.Request.Context()
			err := h.service.BackupDbAndLogError(ctx)
			if err != nil {
				c.String(http.StatusInternalServerError, "backup failed: %v", err)
				return
			}
			log.Printf("backup success")
		}
		c.Status(http.StatusOK)
	}
}

func (h *HttpHandlers) CleanUpFakeNews(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != key {
			c.AbortWithStatus(401)
			return
		}
		fireAndForget := c.Query("fireAndForget") == "true"
		if fireAndForget {
			go h.service.CleanUpFakeNewsAndLogError(context.Background())
		} else {
			ctx := c.Request.Context()
			err := h.service.CleanUpFakeNews(ctx)
			if err != nil {
				c.String(http.StatusInternalServerError, "fake news clean up failed: %v", err)
				return
			}
			log.Printf("fake news clean up success")
		}
		c.Status(http.StatusOK)
	}
}

func (h *HttpHandlers) RebuildIndex(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != key {
			c.AbortWithStatus(401)
			return
		}
		var maxLookBack *time.Time
		maxLookBackStr := c.Query("maxLookBack")
		if maxLookBackStr != "" {
			_maxLookBack, err := time.Parse(time.RFC3339, maxLookBackStr)
			if err != nil {
				log.Printf("error parsing max lookback str %v: %v", maxLookBackStr, err)
				c.AbortWithError(http.StatusBadRequest, err)
				return
			}
			maxLookBack = &_maxLookBack
		}
		go h.service.AddMissingItemsToSearchIndexAndLogError(context.Background(), maxLookBack)
		c.Status(http.StatusOK)
	}
}

var noAutoGenerateSites map[int]any = map[int]any{8: struct{}{} /* DR */, 19: struct{}{} /* TV2 */}

func (h *HttpHandlers) AutoGenerateFakeNews(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != key {
			c.AbortWithStatus(401)
			return
		}
		ctx := context.Background()
		allSites, err := h.service.GetSiteInfos()
		if err != nil {
			log.Printf("error site infos: %v", err)
			c.JSON(500, err.Error())
			return
		}
		latestFakeNews, err := h.service.GetRecentFakeNews(3, nil)
		if err != nil {
			log.Printf("error getting recent fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}
		latestFakeNewsSites := make(map[int]any, len(latestFakeNews))
		for _, fn := range latestFakeNews {
			latestFakeNewsSites[fn.SiteId] = struct{}{}
		}
		sites := make([]RssUrlDto, 0)
		for _, site := range allSites {
			if site.Disabled {
				continue
			}
			_, isInLatest := latestFakeNewsSites[site.Id]
			if isInLatest {
				continue
			}
			_, isNoAutoGenerateSite := noAutoGenerateSites[site.Id]
			if isNoAutoGenerateSite {
				continue
			}
			sites = append(sites, site)
		}
		if len(sites) == 0 {
			c.JSON(500, "sites list was empty")
			return
		}
		site := lo.Sample(sites)
		recentArticleTitles, err := h.service.GetRecentTitles(ctx, site, 10, true)
		if err != nil {
			log.Printf("error getting recent article titles: %v", err)
			c.JSON(500, err.Error())
			return
		}
		var temperature float32 = 1
		var generatedTitleCount = 30
		generatedArticleTitles, err := h.openaiClient.GenerateArticleTitlesList(ctx, site.Name, site.DescriptionEn, recentArticleTitles, generatedTitleCount, temperature)
		if err != nil {
			log.Printf("error getting generated article titles: %v", err)
			c.JSON(500, err.Error())
			return
		}
		log.Printf("generated titles: %v", strings.Join(generatedArticleTitles, ", "))
		selectedTitle, err := h.openaiClient.SelectBestArticleTitle(ctx, site.Name, site.DescriptionEn, generatedArticleTitles)
		if err != nil {
			log.Printf("error selecting best article title: %v", err)
			c.JSON(500, err.Error())
			return
		}
		log.Printf("selected title: %v", selectedTitle)

		err = h.service.CreateFakeNews(site.Id, selectedTitle)
		if err != nil {
			log.Printf("error creating fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}

		articleImgPromise := pkg.NewPromise(func() (string, error) {
			imgUrl, err := h.openaiClient.GenerateImage(ctx, site.Name, site.DescriptionEn, selectedTitle, true)
			if err != nil {
				log.Printf("error making fake news img: %v", err)
			}
			if imgUrl != "" {
				h.service.SetFakeNewsImgUrl(site.Id, selectedTitle, imgUrl)
			}
			return imgUrl, err
		})

		articleContent, err := h.openaiClient.GenerateArticleContentStr(ctx, site.Name, site.DescriptionEn, selectedTitle, temperature)
		if err != nil {
			log.Printf("error generating article content: %v", err)
			c.JSON(500, err.Error())
			return
		}

		err = h.service.UpdateFakeNews(site.Id, selectedTitle, articleContent)
		if err != nil {
			log.Printf("error updating fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}

		log.Printf("waiting for img...")
		articleImgPromise.Get()
		log.Printf("img done!")

		err = h.service.SetFakeNewsHighlighted(site.Id, selectedTitle, true)
		if err != nil {
			log.Printf("error setting highlighted: %v", err)
			c.JSON(500, err.Error())
			return
		}

		createdFakeNews, err := h.service.GetFakeNews(site.Id, selectedTitle)
		if err != nil {
			log.Printf("error getting fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}
		if createdFakeNews == nil {
			log.Printf("fake news was nil")
			c.JSON(500, "fake new was nil")
			return
		}

		c.JSON(200, *createdFakeNews)
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

const (
	ImageStatusGenerating = "GENERATING"
	ImageStatusReady      = "READY"
)

type ContentEvent struct {
	Content     string
	Cursor      string `json:"cursor"`
	ImageUrl    string `json:"imageUrl"`
	ImageStatus string `json:"imageStatus"`
}

func (h *HttpHandlers) HandleGenerateTitles(c *gin.Context) {
	siteName := c.Query("siteName")
	if siteName == "" {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	defaultLimit := 300
	limit := ginutils.IntQuery(c, "limit", defaultLimit)
	if limit > defaultLimit {
		limit = defaultLimit
	}
	temperature := ginutils.Float32Query(c, "temperature", 0.5)
	if temperature > 1 {
		temperature = 1
	}
	if temperature < 0 {
		temperature = 0
	}
	cursorQuery := int64(ginutils.IntQuery(c, "cursor", 0))
	var insertedAtOffset *time.Time
	if cursorQuery > 0 {
		_insertedAtOffset := time.Unix(cursorQuery, 0)
		insertedAtOffset = &_insertedAtOffset
	}
	siteInfo, err := h.service.GetSiteInfo(siteName)
	if err != nil {
		log.Printf("get site info failed: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	if siteInfo == nil {
		c.JSON(http.StatusBadRequest, nil)
		return
	}

	items, err := h.service.repository.GetRecentItems(c.Request.Context(), siteInfo.Id, limit, insertedAtOffset)
	if err != nil {
		log.Printf("get items failed: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusNotFound, nil)
		return
	}
	cursor := fmt.Sprintf("%v", items[len(items)-1].InsertedAt.Unix())
	itemTitles := make([]string, len(items))
	for i, item := range items {
		itemTitles[i] = item.Title
	}
	rand.Shuffle(len(itemTitles), func(i, j int) { itemTitles[i], itemTitles[j] = itemTitles[j], itemTitles[i] })
	stream, err := h.openaiClient.GenerateArticleTitles(c.Request.Context(), siteName, siteInfo.DescriptionEn, itemTitles, 10, temperature)
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
						err = h.service.CreateFakeNews(siteInfo.Id, title)
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
			sb.WriteString(response.Content())
			contentEvent := ContentEvent{
				Content: response.Content(),
				Cursor:  cursor,
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
	siteInfo, err := h.service.GetSiteInfo(siteName)
	if err != nil {
		log.Printf("error getting site info: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
	}
	if siteInfo == nil {
		c.JSON(http.StatusBadRequest, nil)
		return
	}
	existing, err := h.service.GetFakeNews(siteInfo.Id, articleTitle)
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
		if existing.ImageUrl != nil && *existing.ImageUrl != "" {
			c.SSEvent("message", ContentEvent{ImageUrl: *existing.ImageUrl, ImageStatus: ImageStatusReady})
		} else {
			c.SSEvent("message", ContentEvent{ImageUrl: "https://static.bjarke.xyz/placeholder.png", ImageStatus: ImageStatusReady})
		}
		c.Stream(func(w io.Writer) bool {
			contentEvent := ContentEvent{
				Content: existing.Content,
			}
			c.SSEvent("message", contentEvent)
			c.Writer.Flush()
			return false
		})
		return
	}

	articleImgPromise := pkg.NewPromise(func() (string, error) {
		imgUrl, err := h.openaiClient.GenerateImage(c.Request.Context(), siteName, siteInfo.DescriptionEn, articleTitle, true)
		if err != nil {
			log.Printf("error making fake news img: %v", err)
		}
		if imgUrl != "" {
			h.service.SetFakeNewsImgUrl(siteInfo.Id, articleTitle, imgUrl)
		}
		return imgUrl, err
	})

	var temperature float32 = 1.0
	stream, err := h.openaiClient.GenerateArticleContent(c.Request.Context(), siteName, siteInfo.Description, articleTitle, temperature)
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
	c.SSEvent("message", ContentEvent{ImageUrl: "https://static.bjarke.xyz/placeholder.png", ImageStatus: ImageStatusGenerating})
	c.Stream(func(w io.Writer) bool {
		imgUrlSent := false
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				articleContent := sb.String()
				err = h.service.UpdateFakeNews(siteInfo.Id, articleTitle, articleContent)
				if err != nil {
					log.Printf("error saving fake news: %v", err)
				}

				if !imgUrlSent {
					imgUrl, err := articleImgPromise.Get()
					if err != nil {
						log.Printf("error getting openai img: %v", err)
					}
					if imgUrl != "" {
						c.SSEvent("message", ContentEvent{ImageUrl: imgUrl, ImageStatus: ImageStatusReady})
						imgUrlSent = true
					}
				}

				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				return false
			}
			sb.WriteString(response.Content())
			contentEvent := ContentEvent{
				Content: response.Content(),
			}
			c.SSEvent("message", contentEvent)
			imgUrl, err, articleImgOk := articleImgPromise.Poll()
			if articleImgOk {
				if err != nil {
					log.Printf("error getting openai img: %v", err)
				}
				if imgUrl != "" {
					c.SSEvent("message", ContentEvent{ImageUrl: imgUrl, ImageStatus: ImageStatusReady})
					imgUrlSent = true
				}
			}
			c.Writer.Flush()
		}
	})
}

type HighlightedFakeNewsResponse struct {
	FakeNews []FakeNewsDto `json:"fakeNews"`
	Cursor   string        `json:"cursor"`
}

func (h *HttpHandlers) GetHighlightedFakeNews(c *gin.Context) {
	cursorQuery := c.Query("cursor")
	var publishedOffset *time.Time
	var votesOffset int
	if cursorQuery != "" {
		cusorQueryParts := strings.Split(cursorQuery, "¤")
		_publishedOffset, err := time.Parse(time.RFC3339Nano, cusorQueryParts[0])
		if err != nil {
			log.Printf("error parsing cursor: %v", err)
		}
		if err == nil {
			publishedOffset = &_publishedOffset
		}
		if len(cusorQueryParts) >= 2 {
			votesOffset, err = strconv.Atoi(cusorQueryParts[1])
			if err != nil {
				log.Printf("error parsing cursor int: %v", err)
			}
		}
	}
	limit := ginutils.IntQuery(c, "limit", 10)
	if limit > 10 {
		limit = 10
	}
	sorting := ginutils.StringQuery(c, "sorting", "popular")
	var fakeNews []FakeNewsDto = []FakeNewsDto{}
	var err error
	if sorting == "popular" {
		fakeNews, err = h.service.GetPopularFakeNews(limit, publishedOffset, votesOffset)
	} else {
		fakeNews, err = h.service.GetRecentFakeNews(limit, publishedOffset)
	}
	if err != nil {
		log.Printf("error getting highlighted fake news: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	if len(fakeNews) == 0 {
		c.JSON(200, HighlightedFakeNewsResponse{
			FakeNews: []FakeNewsDto{},
			Cursor:   "",
		})
		return
	}
	lastFakeNews := fakeNews[len(fakeNews)-1]
	cursor := fmt.Sprintf("%v¤%v", lastFakeNews.Published.Format(time.RFC3339Nano), lastFakeNews.Votes)
	response := HighlightedFakeNewsResponse{
		FakeNews: fakeNews,
		Cursor:   cursor,
	}
	c.JSON(200, response)
}

func (h *HttpHandlers) SetHighlightedFakeNews(c *gin.Context) {
	auth := c.Request.FormValue("password")
	isAdmin := false
	if auth == h.context.Config.AdminPassword {
		isAdmin = true
	}
	siteName := c.Request.FormValue("siteName")
	title := strings.TrimSpace(c.Request.FormValue("title"))
	siteInfo, err := h.service.GetSiteInfo(siteName)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if siteInfo == nil {
		c.JSON(400, "site not found")
		return
	}
	existing, err := h.service.GetFakeNews(siteInfo.Id, title)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if existing == nil {
		c.JSON(400, "fake news not found")
		return
	}
	// only admin can set a fake news highlighted = false
	var newHighlighted bool
	if existing.Highlighted && isAdmin {
		newHighlighted = false
	} else {
		newHighlighted = !existing.Highlighted
	}
	err = h.service.SetFakeNewsHighlighted(siteInfo.Id, title, newHighlighted)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	existing.Highlighted = newHighlighted
	c.JSON(200, existing)
}

func (h *HttpHandlers) ResetFakeNewsContent(c *gin.Context) {
	auth := c.Request.FormValue("password")
	if auth != h.context.Config.AdminPassword {
		c.Status(http.StatusUnauthorized)
		return
	}
	siteName := c.Request.FormValue("siteName")
	title := strings.TrimSpace(c.Request.FormValue("title"))
	siteInfo, err := h.service.GetSiteInfo(siteName)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if siteInfo == nil {
		c.JSON(400, "site not found")
		return
	}
	existing, err := h.service.GetFakeNews(siteInfo.Id, title)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if existing == nil {
		c.JSON(400, "fake news not found")
		return
	}
	err = h.service.ResetFakeNewsContent(siteInfo.Id, title)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	c.Status(204)
}

func (h *HttpHandlers) HandleArticleVote(c *gin.Context) {
	siteName := c.Request.FormValue("siteName")
	title := strings.TrimSpace(c.Request.FormValue("title"))
	up := c.Request.FormValue("direction") == "up"
	vote := -1
	if up {
		vote = 1
	}
	siteInfo, err := h.service.GetSiteInfo(siteName)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if siteInfo == nil {
		c.JSON(400, "site not found")
		return
	}
	existing, err := h.service.GetFakeNews(siteInfo.Id, title)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if existing == nil {
		c.JSON(400, "fake news not found")
		return
	}
	updatedVotes, err := h.service.VoteFakeNews(siteInfo.Id, title, vote)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	existing.Votes = updatedVotes
	c.JSON(200, existing)
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

func (h *HttpHandlers) GetFakeNewsArticle(c *gin.Context) {
	siteId := ginutils.IntQuery(c, "siteId", 0)
	title := ginutils.StringQuery(c, "title", "")
	fakeNews, err := h.service.GetFakeNews(siteId, title)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	if fakeNews == nil {
		c.JSON(400, "fake news not found")
		return
	}
	c.JSON(200, fakeNews)
}
