package web

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/gin-gonic/gin"
)

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (w *web) GetChartdata(ctx context.Context, query string) (core.ChartsResult, error) {
	isRasende := query == "rasende"

	siteCountPromise := pkg.NewPromise(func() ([]core.SiteCount, error) {
		return w.service.GetSiteCountForSearchQuery(ctx, query, false)
	})

	now := time.Now()
	sevenDaysAgo := now.Add(-time.Hour * 24 * 6)
	tomorrow := now.Add(time.Hour * 24)
	itemCount, err := w.service.GetItemCountForSearchQuery(ctx, query, false, &sevenDaysAgo, &tomorrow, "published")
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		return core.ChartsResult{}, err
	}

	siteCount, err := siteCountPromise.Get()
	if err != nil {
		log.Printf("failed to get site count with query %v: %v", query, err)
		return core.ChartsResult{}, err
	}

	lineTitle := "Den seneste uges raserier"
	lineDatasetLabel := "Raseriudbrud"
	doughnutTitle := "Raseri i de forskellige medier"
	if !isRasende {
		lineTitle = "Den seneste uges brug af '" + query + "'"
		lineDatasetLabel = "Antal '" + query + "'"
		doughnutTitle = "Brug af '" + query + "' i de forskellige medier"
	}
	chartsResult := core.ChartsResult{
		Charts: []core.ChartResult{
			core.MakeLineChartFromSearchQueryCount(itemCount, lineTitle, lineDatasetLabel),
			core.MakeDoughnutChartFromSiteCount(siteCount, doughnutTitle),
		},
	}
	return chartsResult, nil
}

func (w *web) HandleGetSearch(c *gin.Context) {
	searchViewModel := components.SearchViewModel{
		Base: w.getBaseModel(c, "Søg | Rasende"),
	}
	c.HTML(http.StatusOK, "", components.Search(searchViewModel))
}

func (w *web) HandlePostSearch(c *gin.Context) {
	// query := c.Query("q")
	ctx := c.Request.Context()
	query := c.Request.FormValue("search")
	offset := IntForm(c, "offset", 0)
	limit := IntForm(c, "limit", 100)
	if limit > 100 {
		limit = 100
	}

	includeCharts := StringForm(c, "include-charts", "") == "on"

	chartsPromise := pkg.NewPromise(func() (core.ChartsResult, error) {
		if includeCharts {
			chartData, err := w.GetChartdata(ctx, query)
			return chartData, err
		} else {
			return core.ChartsResult{}, nil
		}
	})

	searchContentStr := StringForm(c, "content", "false")
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
		SearchResults: core.SearchResult{
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
