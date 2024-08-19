package handlers

import (
	"context"
	"log"
	"net/http"
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
	c.HTML(http.StatusOK, "", components.FakeNews(w.getBaseModel(c, "Fake News | Rasende")))
}
