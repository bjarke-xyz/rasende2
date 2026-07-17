package web

import (
	"log/slog"
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
)

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (h *web) HandleGetSearch(w http.ResponseWriter, r *http.Request) {
	l := LangOf(r)
	searchViewModel := components.SearchViewModel{
		Base: h.getBaseModel(w, r, l.T("page.search")),
	}
	h.renderer.Page(w, r, http.StatusOK, "search", searchViewModel.Base, searchViewModel)
}

func (h *web) HandlePostSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l := LangOf(r)
	query := r.FormValue("search")
	offset := httpx.IntForm(r, "offset", 0)
	limit := min(httpx.IntForm(r, "limit", 100), 100)

	includeCharts := httpx.StringForm(r, "include-charts", "") == "on"

	chartsPromise := pkg.NewPromise(func() (core.ChartsResult, error) {
		if includeCharts {
			return h.appContext.Deps.Service.GetChartData(ctx, l, query)
		} else {
			return core.ChartsResult{}, nil
		}
	})

	searchContentStr := httpx.StringForm(r, "content", "false")
	searchContent := searchContentStr == "on"
	orderBy := allowedOrderBys[0]
	results, err := h.appContext.Deps.Service.SearchItems(ctx, l, query, searchContent, offset, limit, orderBy)
	if err != nil {
		slog.Error("getting items failed", "query", query, "error", err)
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if len(results) > limit {
		results = results[0:limit]
	}
	chartsResult, err := chartsPromise.Get()
	if err != nil {
		slog.Error("getting charts failed", "query", query, "error", err)
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
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
	h.renderer.Partial(w, r, http.StatusOK, "searchResults", searchResultsModel)
}
