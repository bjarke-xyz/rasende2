package handlers

import (
	"log"
	"net/http"

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

func getBaseModel(c *gin.Context) components.BaseViewModel {
	return components.BaseViewModel{
		Path: c.Request.URL.Path,
	}
}

var allowedOrderBys = []string{"-published", "published", "-_score", "_score"}

func (w *WebHandlers) IndexHandler(c *gin.Context) {
	indexModel := components.IndexModel{
		Base: getBaseModel(c),
	}

	query := "rasende"
	offset := 0
	limit := 10
	searchContent := false
	orderBy := allowedOrderBys[0]
	results, err := w.service.SearchItems(c.Request.Context(), query, searchContent, offset, limit, orderBy)
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
	c.HTML(http.StatusOK, "", components.Index(indexModel))
}
