package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bjarke-xyz/rasende2/ai"
	"github.com/bjarke-xyz/rasende2/config"
	"github.com/bjarke-xyz/rasende2/db"
	"github.com/bjarke-xyz/rasende2/db/dao"
	"github.com/bjarke-xyz/rasende2/ginutils"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/bjarke-xyz/rasende2/rss"
	"github.com/bjarke-xyz/rasende2/web/auth"
	"github.com/bjarke-xyz/rasende2/web/components"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/sashabaranov/go-openai"
)

type WebHandlers struct {
	context      *pkg.AppContext
	service      *rss.RssService
	openaiClient *ai.OpenAIClient
	search       *rss.RssSearch
}

func NewWebHandlers(context *pkg.AppContext, service *rss.RssService, openaiClient *ai.OpenAIClient, search *rss.RssSearch) *WebHandlers {
	return &WebHandlers{
		context:      context,
		service:      service,
		openaiClient: openaiClient,
		search:       search,
	}
}

func (w *WebHandlers) getBaseModel(c *gin.Context, title string) components.BaseViewModel {
	var unixBuildTime int64 = 0
	if w.context.Config.BuildTime != nil {
		unixBuildTime = w.context.Config.BuildTime.Unix()
	} else {
		unixBuildTime = time.Now().Unix()
	}
	hxRequest := c.Request.Header.Get("HX-Request")
	includeLayout := hxRequest == "" || hxRequest == "false"
	log.Println("hxRequest", hxRequest, "includeLayout", includeLayout)
	userId, ok := auth.GetUserId(c)
	model := components.BaseViewModel{
		Path:            c.Request.URL.Path,
		UnixBuildTime:   unixBuildTime,
		Title:           title,
		IncludeLayout:   includeLayout,
		FlashInfo:       ginutils.GetFlashes(c, ginutils.FlashTypeInfo),
		FlashWarn:       ginutils.GetFlashes(c, ginutils.FlashTypeWarn),
		FlashError:      ginutils.GetFlashes(c, ginutils.FlashTypeError),
		NoCache:         c.Request.URL.Query().Get("nocache") == "true",
		UserId:          userId,
		IsAnonymousUser: !ok,
		IsAdmin:         auth.IsAdmin(c),
	}
	return model
}

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (w *WebHandlers) HandleGetIndex(c *gin.Context) {
	indexModel := components.IndexModel{
		Base: w.getBaseModel(c, "Raseri i de danske medier"),
	}
	ctx := c.Request.Context()
	indexPageData, err := w.service.GetIndexPageData(ctx, indexModel.Base.NoCache)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: indexModel.Base, Err: err}))
		return
	}
	indexModel.SearchResults = *indexPageData.SearchResult
	indexModel.ChartsResult = *indexPageData.ChartsResult
	c.HTML(http.StatusOK, "", components.Index(indexModel))
}

func (w *WebHandlers) GetChartdata(ctx context.Context, query string) (rss.ChartsResult, error) {
	isRasende := query == "rasende"

	siteCountPromise := pkg.NewPromise(func() ([]rss.SiteCount, error) {
		return w.service.GetSiteCountForSearchQuery(ctx, query, false)
	})

	now := time.Now()
	sevenDaysAgo := now.Add(-time.Hour * 24 * 6)
	tomorrow := now.Add(time.Hour * 24)
	itemCount, err := w.service.GetItemCountForSearchQuery(ctx, query, false, &sevenDaysAgo, &tomorrow, "published")
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		return rss.ChartsResult{}, err
	}

	siteCount, err := siteCountPromise.Get()
	if err != nil {
		log.Printf("failed to get site count with query %v: %v", query, err)
		return rss.ChartsResult{}, err
	}

	lineTitle := "Den seneste uges raserier"
	lineDatasetLabel := "Raseriudbrud"
	doughnutTitle := "Raseri i de forskellige medier"
	if !isRasende {
		lineTitle = "Den seneste uges brug af '" + query + "'"
		lineDatasetLabel = "Antal '" + query + "'"
		doughnutTitle = "Brug af '" + query + "' i de forskellige medier"
	}
	chartsResult := rss.ChartsResult{
		Charts: []rss.ChartResult{
			rss.MakeLineChartFromSearchQueryCount(itemCount, lineTitle, lineDatasetLabel),
			rss.MakeDoughnutChartFromSiteCount(siteCount, doughnutTitle),
		},
	}
	return chartsResult, nil
}

func (w *WebHandlers) HandleGetSearch(c *gin.Context) {
	searchViewModel := components.SearchViewModel{
		Base: w.getBaseModel(c, "Søg | Rasende"),
	}
	c.HTML(http.StatusOK, "", components.Search(searchViewModel))
}

func (w *WebHandlers) HandlePostSearch(c *gin.Context) {
	// query := c.Query("q")
	ctx := c.Request.Context()
	query := c.Request.FormValue("search")
	offset := ginutils.IntForm(c, "offset", 0)
	limit := ginutils.IntForm(c, "limit", 100)
	if limit > 100 {
		limit = 100
	}

	includeCharts := ginutils.StringForm(c, "include-charts", "") == "on"

	chartsPromise := pkg.NewPromise(func() (rss.ChartsResult, error) {
		if includeCharts {
			chartData, err := w.GetChartdata(ctx, query)
			return chartData, err
		} else {
			return rss.ChartsResult{}, nil
		}
	})

	searchContentStr := ginutils.StringForm(c, "content", "false")
	searchContent := searchContentStr == "on"
	orderBy := allowedOrderBys[0]
	results, err := w.service.SearchItems(ctx, query, searchContent, offset, limit, orderBy)
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	chartsResult, err := chartsPromise.Get()
	if err != nil {
		log.Printf("failed to get charts with query %v: %v", query, err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	searchResultsModel := components.SearchResultsViewModel{
		SearchResults: rss.SearchResult{
			HighlightedWords: []string{query},
			Items:            results,
		},
		ChartsResult:  chartsResult,
		NextOffset:    offset + limit,
		Search:        query,
		IncludeCharts: includeCharts,
	}
	c.HTML(http.StatusOK, "", components.SearchResults(searchResultsModel))

}

func (w *WebHandlers) HandleGetFakeNews(c *gin.Context) {
	title := "Fake News | Rasende"
	onlyGrid := ginutils.StringForm(c, "only-grid", "false") == "true"
	cursorQuery := c.Query("cursor")
	var publishedOffset *time.Time
	var votesOffset int
	if cursorQuery != "" {
		cursorQueryParts := strings.Split(cursorQuery, "¤")
		_publishedOffset, err := time.Parse(time.RFC3339Nano, cursorQueryParts[0])
		if err != nil {
			log.Printf("error parsing cursor: %v", err)
		}
		if err == nil {
			publishedOffset = &_publishedOffset
		}
		if len(cursorQueryParts) >= 2 {
			votesOffset, err = strconv.Atoi(cursorQueryParts[1])
			if err != nil {
				log.Printf("error parsing cursor int: %v", err)
			}
		}
	}
	limit := ginutils.IntQuery(c, "limit", 5)
	if limit > 5 {
		limit = 5
	}
	sorting := ginutils.StringQuery(c, "sorting", "popular")
	var fakeNews []rss.FakeNewsDto = []rss.FakeNewsDto{}
	var err error
	if sorting == "popular" {
		fakeNews, err = w.service.GetPopularFakeNews(limit, publishedOffset, votesOffset)
	} else {
		fakeNews, err = w.service.GetRecentFakeNews(limit, publishedOffset)
	}
	if err != nil {
		log.Printf("error getting highlighted fake news: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if len(fakeNews) == 0 {
		model := components.FakeNewsViewModel{
			Base:     w.getBaseModel(c, title),
			FakeNews: []rss.FakeNewsDto{},
			OnlyGrid: onlyGrid,
		}
		c.HTML(http.StatusOK, "", components.FakeNews(model))
		return
	}
	lastFakeNews := fakeNews[len(fakeNews)-1]
	cursor := fmt.Sprintf("%v¤%v", lastFakeNews.Published.Format(time.RFC3339Nano), lastFakeNews.Votes)
	// If returned items is less than limit, return blank cursor so we dont request an empty list on next request
	if len(fakeNews) < limit {
		cursor = ""
	}
	alreadyVoted := getAlreadyVoted(c)
	model := components.FakeNewsViewModel{
		Base:         w.getBaseModel(c, title),
		FakeNews:     fakeNews,
		Cursor:       cursor,
		Sorting:      sorting,
		OnlyGrid:     onlyGrid,
		AlreadyVoted: alreadyVoted,
		Funcs: components.ArticleFuncsModel{
			TimeDifference: getTimeDifference,
			TruncateText:   truncateText,
		},
	}
	c.HTML(http.StatusOK, "", components.FakeNews(model))
}

func (w *WebHandlers) HandleGetFakeNewsArticle(c *gin.Context) {
	slug, _ := url.QueryUnescape(c.Param("slug"))
	siteId, date, title, err := parseArticleSlug(slug)
	if err != nil {
		log.Printf("error parsing slug '%v': %v", slug, err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	fakeNewsDto, err := w.service.GetFakeNews(siteId, title)
	if err != nil {
		log.Printf("error getting fake news: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if fakeNewsDto == nil {
		err = fmt.Errorf("fake news not found")
		log.Printf("error getting fake news: %v", err)
		c.HTML(http.StatusNotFound, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if fakeNewsDto.Published.Format(time.DateOnly) != date.Format(time.DateOnly) {
		err = fmt.Errorf("invalid date. Got=%v, expected=%v", date, fakeNewsDto.Published)
		log.Printf("returned error because of dates: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	fakeNewsArticleViewModel := components.FakeNewsArticleViewModel{
		Base:     w.getBaseModel(c, fmt.Sprintf("%s | %v | Fake News", fakeNewsDto.Title, fakeNewsDto.SiteName)),
		FakeNews: *fakeNewsDto,
	}
	url := fmt.Sprintf("https://%v%v", c.Request.Host, c.Request.URL.Path)
	fakeNewsArticleViewModel.Base.OpenGraph = &components.BaseOpenGraphModel{
		Title:       fmt.Sprintf("%v | %v", fakeNewsDto.Title, fakeNewsDto.SiteName),
		Image:       *fakeNewsDto.ImageUrl,
		Url:         url,
		Description: truncateText(fakeNewsDto.Content, 100),
	}
	c.HTML(http.StatusOK, "", components.FakeNewsArticle(fakeNewsArticleViewModel))
}

func (w *WebHandlers) HandleGetTitleGenerator(c *gin.Context) {
	title := "Title Generator | Rasende"
	selectedSiteId := ginutils.IntQuery(c, "siteId", 0)

	sites, err := w.service.GetRssUrls()
	if err != nil {
		log.Printf("error getting sites: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	var selectedSite rss.RssUrlDto
	if selectedSiteId > 0 {
		_selectedSite, ok := lo.Find(sites, func(s rss.RssUrlDto) bool { return s.Id == selectedSiteId })
		if ok {
			selectedSite = _selectedSite
		}
	}

	c.HTML(http.StatusOK, "", components.TitleGenerator(components.TitleGeneratorViewModel{
		Base:           w.getBaseModel(c, title),
		Sites:          sites,
		SelectedSiteId: selectedSiteId,
		SelectedSite:   selectedSite,
	}))
}

func (w *WebHandlers) HandleGetSseTitles(c *gin.Context) {
	ctx := c.Request.Context()
	siteId := ginutils.IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	defaultLimit := 10
	limit := ginutils.IntQuery(c, "limit", defaultLimit)
	if limit > defaultLimit {
		limit = defaultLimit
	}
	var temperature float32 = 1.0
	cursorQuery := int64(ginutils.IntQuery(c, "cursor", 0))
	var insertedAtOffset *time.Time
	if cursorQuery > 0 {
		_insertedAtOffset := time.Unix(cursorQuery, 0).UTC()
		insertedAtOffset = &_insertedAtOffset
	}
	log.Println("insertedAtOffset", insertedAtOffset)
	siteInfo, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if siteInfo == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found"), DoNotIncludeLayout: true}))
		return
	}

	items, err := w.service.GetRecentItems(ctx, siteId, limit, insertedAtOffset)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	log.Println("items", items[len(items)-1])
	cursor := fmt.Sprintf("%v", items[len(items)-1].InsertedAt.Unix())
	log.Println("cursor", cursor)
	itemTitles := make([]string, len(items))
	for i, item := range items {
		itemTitles[i] = item.Title
	}
	rand.Shuffle(len(itemTitles), func(i, j int) { itemTitles[i], itemTitles[j] = itemTitles[j], itemTitles[i] })
	stream, err := w.openaiClient.GenerateArticleTitles(c.Request.Context(), siteInfo.Name, siteInfo.DescriptionEn, itemTitles, 10, temperature)
	if err != nil {
		log.Printf("openai failed: %v", err)

		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("try again later"), DoNotIncludeLayout: true}))
		} else {
			c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		}
		return
	}

	titles := []string{}
	var currentTitle strings.Builder
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "close")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(io.Writer) bool {
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				for _, title := range titles {
					if len(title) > 0 {
						err = w.service.CreateFakeNews(siteInfo.Id, title)
						if err != nil {
							log.Printf("create fake news failed for site %v, title %v: %v", siteInfo.Name, title, err)
						}
					}
				}
				c.SSEvent("button", ginutils.RenderToStringCtx(ctx, components.ShowMoreTitlesButton(siteInfo.Id, cursor)))
				c.SSEvent("sse-close", "sse-close")
				c.Writer.Flush()
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				return false
			}
			content := response.Content()
			for _, ch := range content {
				if ch == '\n' {
					title := strings.TrimSpace(currentTitle.String())
					titles = append(titles, title)

					c.SSEvent("title", ginutils.RenderToStringCtx(ctx, components.GeneratedTitleLink(siteInfo.Id, title)))
					c.Writer.Flush()
					currentTitle.Reset()
				} else {
					currentTitle.WriteRune(ch)
				}
			}
		}
	})
}

func (w *WebHandlers) HandleGetTitleGeneratorSse(c *gin.Context) {
	siteId := ginutils.IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	cursor := ginutils.StringQuery(c, "cursor", "")
	c.HTML(http.StatusOK, "", components.TitlesSse(siteId, cursor, false))
}

func (w *WebHandlers) HandleGetArticleGenerator(c *gin.Context) {
	log.Println(c.Request.URL.Query())
	pageTitle := "Article Generator | Fake News"
	siteId := ginutils.IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId")}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId)}))
		return
	}
	articleTitle := ginutils.StringQuery(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title")}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle)}))
		return
	}

	model := components.ArticleGeneratorViewModel{
		Base:             w.getBaseModel(c, pageTitle),
		Site:             *site,
		Article:          *article,
		ImagePlaceholder: config.PlaceholderImgUrl,
	}
	c.HTML(http.StatusOK, "", components.ArticleGenerator(model))
}

func (w *WebHandlers) HandleGetSseArticleContent(c *gin.Context) {
	siteId := ginutils.IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId), DoNotIncludeLayout: true}))
		return
	}
	articleTitle := ginutils.StringQuery(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title"), DoNotIncludeLayout: true}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle), DoNotIncludeLayout: true}))
		return
	}

	if len(article.Content) > 0 {
		log.Printf("found existing fake news for site %v title %v", site.Name, article.Title)
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "close")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		if article.ImageUrl != nil && *article.ImageUrl != "" {
			c.SSEvent("image", ginutils.RenderToString(c, components.ArticleImg(*article.ImageUrl, article.Title)))
		} else {
			c.SSEvent("image", ginutils.RenderToString(c, components.ArticleImg(config.PlaceholderImgUrl, article.Title)))
		}
		c.Stream(func(w io.Writer) bool {
			sseContent := strings.ReplaceAll(article.Content, "\n", "<br />")
			c.SSEvent("content", sseContent)
			c.SSEvent("sse-close", "sse-close")
			c.Writer.Flush()
			return false
		})
		return
	}

	articleImgPromise := pkg.NewPromise(func() (string, error) {
		imgUrl, err := w.openaiClient.GenerateImage(c.Request.Context(), site.Name, site.DescriptionEn, article.Title, true)
		if err != nil {
			log.Printf("error maing fake news img: %v", err)
		}
		if imgUrl != "" {
			w.service.SetFakeNewsImgUrl(site.Id, article.Title, imgUrl)
		}
		return imgUrl, err
	})

	var temperature float32 = 1.0
	stream, err := w.openaiClient.GenerateArticleContent(c.Request.Context(), site.Name, site.Description, article.Title, temperature)
	if err != nil {
		log.Printf("openai failed: %v", err)
		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			c.HTML(http.StatusTooManyRequests, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		} else {
			c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		}
		return
	}

	var sb strings.Builder
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "close")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(io.Writer) bool {
		imgUrlSent := false
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				articleContent := sb.String()
				err = w.service.UpdateFakeNews(site.Id, articleTitle, articleContent)
				if err != nil {
					log.Printf("error saving fake news: %v", err)
				}
				if !imgUrlSent {
					imgUrl, err := articleImgPromise.Get()
					if err != nil {
						log.Printf("error getting openai img: %v", err)
					}
					if imgUrl != "" {
						c.SSEvent("image", ginutils.RenderToString(c, components.ArticleImg(imgUrl, article.Title)))
						imgUrlSent = true
					}
				}
				c.SSEvent("sse-close", "sse-close")
				c.Writer.Flush()
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				c.SSEvent("sse-close", "sse-close")
				c.Writer.Flush()
				return false
			}
			content := response.Content()
			sb.WriteString(content)
			sseContent := fmt.Sprintf("<span>%v</span>", strings.ReplaceAll(content, "\n", "<br />"))
			c.SSEvent("content", sseContent)
			if !imgUrlSent {
				imgUrl, err, articleImgOk := articleImgPromise.Poll()
				if articleImgOk {
					if err != nil {
						log.Printf("error getting openai img: %v", err)
					}
					if imgUrl != "" {
						c.SSEvent("image", ginutils.RenderToString(c, components.ArticleImg(imgUrl, article.Title)))
						imgUrlSent = true
					}
				}
			}
			c.Writer.Flush()
		}
	})
}

func (w *WebHandlers) HandlePostPublishFakeNews(c *gin.Context) {
	isAdmin := auth.IsAdmin(c)
	siteId := ginutils.IntForm(c, "siteId", 0)
	// TODO: instead of returning html with error, do redirect with flash error
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId), DoNotIncludeLayout: true}))
		return
	}
	articleTitle := ginutils.StringForm(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title"), DoNotIncludeLayout: true}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle), DoNotIncludeLayout: true}))
		return
	}

	// only admin can set a fake news highlighted = false
	var newHighlighted bool
	if article.Highlighted && isAdmin {
		newHighlighted = false
	} else {
		newHighlighted = !article.Highlighted
	}
	err = w.service.SetFakeNewsHighlighted(site.Id, article.Title, newHighlighted)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	article.Highlighted = newHighlighted
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/fake-news/%v", article.Slug()))
}

func (w *WebHandlers) HandlePostResetContent(c *gin.Context) {
	redirectPath := ginutils.RefererOrDefault(c, "/")
	if !auth.IsAdmin(c) {
		ginutils.AddFlashWarn(c, "Requires admin")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	siteId := ginutils.IntForm(c, "siteId", 0)
	if siteId == 0 {
		ginutils.AddFlashWarn(c, "missing site")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	title := ginutils.StringForm(c, "title", "")
	if title == "" {
		ginutils.AddFlashWarn(c, "missing title")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	err := w.service.ResetFakeNewsContent(siteId, title)
	if err != nil {
		ginutils.AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}

	c.Redirect(http.StatusSeeOther, redirectPath)
}

func (w *WebHandlers) HandlePostArticleVote(c *gin.Context) {
	siteId := ginutils.IntForm(c, "siteId", 0)
	// TODO: instead of returning html with error, do redirect with error query
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId), DoNotIncludeLayout: true}))
		return
	}
	articleTitle := ginutils.StringForm(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title"), DoNotIncludeLayout: true}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle), DoNotIncludeLayout: true}))
		return
	}

	direction := c.Request.FormValue("direction")
	if direction != "up" && direction != "down" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid vote %v", direction), DoNotIncludeLayout: true}))
	}
	up := direction == "up"
	vote := -1
	if up {
		vote = 1
	}

	updatedVotes, err := w.service.VoteFakeNews(site.Id, article.Title, vote)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	article.Votes = updatedVotes
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(fmt.Sprintf("VOTED.%v", article.Id()), direction, 3600*24, "/", "", true, true)
	alreadyVoted := getAlreadyVoted(c)
	alreadyVoted[article.Id()] = direction
	c.HTML(http.StatusOK, "", components.FakeNewsVotes(*article, alreadyVoted))
}

func (w *WebHandlers) HandleGetLogin(c *gin.Context) {
	showOtp := c.Query("otp") == "true"
	email := c.Query("email")
	returnPath := c.Query("returnpath")
	c.HTML(http.StatusOK, "", components.Login(components.LoginViewModel{
		Base:       w.getBaseModel(c, "Login | Rasende"),
		OTP:        showOtp,
		Email:      email,
		ReturnPath: returnPath,
	}))
}

func (w *WebHandlers) HandlePostLogin(c *gin.Context) {
	ctx := c.Request.Context()
	successPath := ginutils.StringForm(c, "returnPath", "/")
	redirectPath := ginutils.RefererOrDefault(c, w.context.Config.BaseUrl+"/login")
	redirectPathUrl, err := url.Parse(redirectPath)
	if err != nil {
		ginutils.AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	email := c.Request.FormValue("email")
	if !strings.Contains(email, "@") {
		ginutils.AddFlashWarn(c, "Invalid email")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	db, err := db.OpenQueries(w.context.Config)
	if err != nil {
		ginutils.AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			user, err = db.CreateUser(ctx, dao.CreateUserParams{
				Email: email,
			})
			if err != nil {
				ginutils.AddFlashError(c, err)
				c.Redirect(http.StatusSeeOther, redirectPath)
				return
			}
		} else {
			ginutils.AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
	}
	// TODO: support password login
	// password := c.Request.FormValue("password")
	formOtp := strings.TrimSpace(strings.ReplaceAll(c.Request.FormValue("otp"), "-", ""))
	if formOtp != "" {
		magicLinks, err := db.GetLinksByUserId(ctx, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				ginutils.AddFlashWarn(c, "Koden virker ikke")
				c.Redirect(http.StatusSeeOther, redirectPath)
				return
			}
			ginutils.AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		for _, magicLink := range magicLinks {
			if pkg.CheckPasswordHash(formOtp, magicLink.OtpHash) {
				db.DeleteMagicLink(ctx, magicLink.ID)
				err = db.SetUserEmailConfirmed(ctx, magicLink.UserID)
				if err != nil {
					log.Printf("error setting user %v email confirmed: %v", magicLink.UserID, err)
				}
				user, err := db.GetUser(ctx, magicLink.UserID)
				if err != nil {
					ginutils.AddFlashError(c, fmt.Errorf("der skete en fejl"))
					log.Printf("failed to get user by id %v: %v", magicLink.UserID, err)
					c.Redirect(http.StatusSeeOther, redirectPath)
					return
				}
				auth.SetUserId(c, user.ID, user.IsAdmin)
				ginutils.AddFlashInfo(c, "Du er nu logget ind!")
				c.Redirect(http.StatusSeeOther, successPath)
				return
			}
		}
		ginutils.AddFlashWarn(c, "Koden virker ikke")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	} else {
		// login link
		otp, err := pkg.GenerateOTP()
		if err != nil {
			ginutils.AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		otpHash, err := pkg.HashPassword(otp)
		if err != nil {
			ginutils.AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		linkCode, err := pkg.GenerateSecureToken()
		if err != nil {
			ginutils.AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		expiresAt := time.Now().Add(15 * time.Minute)
		db.CreateMagicLink(ctx, dao.CreateMagicLinkParams{
			UserID:    user.ID,
			OtpHash:   otpHash,
			LinkCode:  linkCode,
			ExpiresAt: expiresAt,
		})
		// TODO: check if user exists before sending mail
		// TODO: reutrn path query param
		w.context.Mail.SendAuthLink(pkg.SendAuthLinkRequest{
			Receiver:            email,
			CodePath:            fmt.Sprintf("/login-link?code=%v&returnpath=%v", linkCode, url.QueryEscape(successPath)),
			OTP:                 otp,
			ExpirationTimestamp: expiresAt,
		})
		ginutils.AddFlashInfo(c, "Tjek din mail!")
		redirectQuery := redirectPathUrl.Query()
		redirectQuery.Set("otp", "true")
		redirectQuery.Set("email", email)
		redirectPathUrl.RawQuery = redirectQuery.Encode()
		c.Redirect(http.StatusSeeOther, redirectPathUrl.String())
	}
}

func (w *WebHandlers) HandleGetLoginLink(c *gin.Context) {
	ctx := c.Request.Context()
	code := c.Query("code")
	successPath := ginutils.StringQuery(c, "returnpath", "/")
	failurePath := "/"
	if code == "" {
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}
	db, err := db.OpenQueries(w.context.Config)
	if err != nil {
		ginutils.AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}
	magicLink, err := db.GetLinkByCode(ctx, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ginutils.AddFlashWarn(c, "Linket virker ikke")
			c.Redirect(http.StatusSeeOther, failurePath)
			return
		}
		ginutils.AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}

	err = db.SetUserEmailConfirmed(ctx, magicLink.UserID)
	if err != nil {
		log.Printf("error setting user %v email confirmed: %v", magicLink.UserID, err)
	}

	err = db.DeleteMagicLink(ctx, magicLink.ID)
	if err != nil {
		log.Printf("error deleting magic link: %v", err)
	}

	user, err := db.GetUser(ctx, magicLink.UserID)
	if err != nil {
		ginutils.AddFlashError(c, fmt.Errorf("der skete en fejl"))
		log.Printf("failed to get user by id %v: %v", magicLink.UserID, err)
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}

	ginutils.AddFlashInfo(c, "Du er nu logget ind!")
	auth.SetUserId(c, user.ID, user.IsAdmin)
	c.Redirect(http.StatusSeeOther, successPath)
}

func (w *WebHandlers) HandlePostLogout(c *gin.Context) {
	redirectPath := c.Request.Header.Get("Referer")
	if redirectPath == "" {
		redirectPath = "/"
	}
	auth.ClearUserId(c)
	ginutils.AddFlashInfo(c, "Du er nu logget ud!")
	c.Redirect(http.StatusSeeOther, redirectPath)
}

func getAlreadyVoted(c *gin.Context) map[string]string {
	cookies := c.Request.Cookies()
	result := make(map[string]string, 0)
	for _, cookie := range cookies {
		name := cookie.Name
		if strings.HasPrefix(name, "VOTED.") {
			nameParts := strings.Split(name, "VOTED.")
			if len(nameParts) >= 2 {
				id := nameParts[1]
				result[id] = cookie.Value
			}
		}
	}
	return result
}

func parseArticleSlug(slug string) (int, time.Time, string, error) {
	// slug = {site-id:123}-{date:2024-08-19}-{title:article title qwerty}
	var err error
	siteId := 0
	date := time.Time{}
	title := ""
	parts := strings.Split(slug, "-")
	log.Println(len(parts), parts)
	if len(parts) < 4 {
		return siteId, date, title, fmt.Errorf("invalid slug")
	}
	siteId, err = strconv.Atoi(parts[0])
	if err != nil {
		return siteId, date, title, fmt.Errorf("error parsing site id: %w", err)
	}

	year := parts[1]
	month := parts[2]
	day := parts[3]
	date, err = time.Parse("2006-01-02", fmt.Sprintf("%v-%v-%v", year, month, day))
	if err != nil {
		return siteId, date, title, fmt.Errorf("error parsing date: %w", err)
	}

	title = strings.Join(parts[4:], "-")
	return siteId, date, title, nil
}

func getTimeDifference(date time.Time) string {
	now := time.Now()
	diff := now.Sub(date)

	switch {
	case diff < time.Minute:
		return fmt.Sprintf("%ds", int(diff.Seconds()))
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	default:
		yearFormat := ""
		if date.Year() != now.Year() {
			yearFormat = " 2006"
		}
		return date.Format("Jan 2" + yearFormat)
	}
}

func truncateText(text string, maxLength int) string {
	lastSpaceIx := maxLength
	len := 0
	for i, r := range text {
		if unicode.IsSpace(r) {
			lastSpaceIx = i
		}
		len++
		if len > maxLength {
			return text[:lastSpaceIx] + "..."
		}
	}
	// If here, string is shorter or equal to maxLen
	return text
}
