package handlers

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/bjarke-xyz/rasende2-api/ai"
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

func (w *WebHandlers) getBaseModel(c *gin.Context) components.BaseViewModel {
	var unixBuildTime int64 = 0
	if w.context.Config.BuildTime != nil {
		unixBuildTime = w.context.Config.BuildTime.Unix()
	} else {
		unixBuildTime = time.Now().Unix()
	}
	return components.BaseViewModel{
		Path:          c.Request.URL.Path,
		UnixBuildTime: unixBuildTime,
	}
}

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (w *WebHandlers) IndexHandler(c *gin.Context) {
	indexModel := components.IndexModel{
		Base: w.getBaseModel(c),
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

func (w *WebHandlers) SearchHandler(c *gin.Context) {
	c.HTML(http.StatusOK, "", components.Search(w.getBaseModel(c)))
}
func (w *WebHandlers) FakeNewsHandler(c *gin.Context) {
	c.HTML(http.StatusOK, "", components.FakeNews(w.getBaseModel(c)))
}
