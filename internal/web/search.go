package web

import (
	"log"
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/gin-gonic/gin"
)

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (w *web) HandleGetSearch(c *gin.Context) {
	l := LangOf(c)
	searchViewModel := components.SearchViewModel{
		Base: w.getBaseModel(c, l.T("page.search")),
	}
	w.renderer.Page(c, http.StatusOK, "search", searchViewModel.Base, searchViewModel)
}

func (w *web) HandlePostSearch(c *gin.Context) {
	ctx := c.Request.Context()
	l := LangOf(c)
	query := c.Request.FormValue("search")
	offset := IntForm(c, "offset", 0)
	limit := IntForm(c, "limit", 100)
	if limit > 100 {
		limit = 100
	}

	includeCharts := StringForm(c, "include-charts", "") == "on"

	chartsPromise := pkg.NewPromise(func() (core.ChartsResult, error) {
		if includeCharts {
			return w.appContext.Deps.Service.GetChartData(ctx, l, query)
		} else {
			return core.ChartsResult{}, nil
		}
	})

	searchContentStr := StringForm(c, "content", "false")
	searchContent := searchContentStr == "on"
	orderBy := allowedOrderBys[0]
	results, err := w.appContext.Deps.Service.SearchItems(ctx, l, query, searchContent, offset, limit, orderBy)
	if err != nil {
		log.Printf("failed to get items with query %v: %v", query, err)
		w.renderErrorFragment(c, http.StatusInternalServerError, err)
		return
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	chartsResult, err := chartsPromise.Get()
	if err != nil {
		log.Printf("failed to get charts with query %v: %v", query, err)
		w.renderErrorFragment(c, http.StatusInternalServerError, err)
		return
	}
	searchResultsModel := components.SearchResultsViewModel{
		SearchResults: core.SearchResult{
			Items: results,
		},
		ChartsResult:  chartsResult,
		NextOffset:    offset + limit,
		Search:        query,
		IncludeCharts: includeCharts,
	}
	w.renderer.Partial(c, http.StatusOK, "searchResults", searchResultsModel)
}
