package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/ginutils"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/bjarke-xyz/rasende2-api/rss"
	"github.com/bjarke-xyz/rasende2-api/web/components"
	"github.com/gin-gonic/gin"
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
	return components.BaseViewModel{
		Path:          c.Request.URL.Path,
		UnixBuildTime: unixBuildTime,
		Title:         title,
	}
}

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (w *WebHandlers) HandleGetIndex(c *gin.Context) {
	indexModel := components.IndexModel{
		Base: w.getBaseModel(c, "Raseri i de danske medier"),
	}

	ctx := c.Request.Context()
	query := "rasende"
	offset := 0
	limit := 10
	searchContent := false
	orderBy := allowedOrderBys[0]

	chartsPromise := pkg.NewPromise(func() (rss.ChartsResult, error) {
		chartData, err := w.GetChartdata(ctx, query)
		return chartData, err
	})

	results, err := w.service.SearchItems(ctx, query, searchContent, offset, limit, orderBy)
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: indexModel.Base, Err: err}))
		return
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	indexModel.SearchResults = rss.SearchResult{
		HighlightedWords: []string{query},
		Items:            results,
	}
	chartsData, err := chartsPromise.Get()
	if err != nil {
		log.Printf("failed to get charts data: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: indexModel.Base, Err: err}))
		return
	}
	indexModel.ChartsResult = chartsData
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
	limit := ginutils.IntForm(c, "limit", 10)
	if limit > 100 {
		limit = 10
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
	cursorQuery := c.Query("cursor")
	var publishedOffset *time.Time
	if cursorQuery != "" {
		_publishedOffset, err := time.Parse(time.RFC3339Nano, cursorQuery)
		if err != nil {
			log.Printf("error parsing cursor: %v", err)
		}
		if err == nil {
			publishedOffset = &_publishedOffset
		}
	}
	limit := ginutils.IntQuery(c, "limit", 10)
	if limit > 10 {
		limit = 10
	}
	highlightedFakeNews, err := w.service.GetHighlightedFakeNews(limit, publishedOffset)
	if err != nil {
		log.Printf("error getting highlighted fake news: %v", err)
		c.JSON(http.StatusInternalServerError, nil)
		return
	}
	if len(highlightedFakeNews) == 0 {
		model := components.FakeNewsViewModel{
			Base:     w.getBaseModel(c, title),
			FakeNews: []rss.FakeNewsDto{},
		}
		c.HTML(http.StatusOK, "", components.FakeNews(model))
		return
	}
	cursor := fmt.Sprintf("%v", highlightedFakeNews[len(highlightedFakeNews)-1].Published.Format(time.RFC3339Nano))
	model := components.FakeNewsViewModel{
		Base:     w.getBaseModel(c, title),
		FakeNews: highlightedFakeNews,
		Cursor:   cursor,
		Funcs: components.ArticleFuncsModel{
			TimeDifference: getTimeDifference,
			TruncateText:   truncateText,
		},
	}
	c.HTML(http.StatusOK, "", components.FakeNews(model))
}

func (w *WebHandlers) HandleGetFakeNewsArticle(c *gin.Context) {
	slug := c.Param("slug")
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
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
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
	url := fmt.Sprintf("http://localhost:%v/%v", w.context.Config.Port, c.Request.URL.Path)
	fakeNewsArticleViewModel.Base.OpenGraph = &components.BaseOpenGraphModel{
		Title:       fmt.Sprintf("%v | %v", fakeNewsDto.Title, fakeNewsDto.SiteName),
		Image:       *fakeNewsDto.ImageUrl,
		Url:         url,
		Description: truncateText(fakeNewsDto.Content, 100),
	}
	c.HTML(http.StatusOK, "", components.FakeNewsArticle(fakeNewsArticleViewModel))
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
	if len(text) <= maxLength {
		return text
	}
	return text[:maxLength] + "..."
}
