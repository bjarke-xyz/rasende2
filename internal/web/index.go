package web

import (
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/gin-gonic/gin"
)

func (w *web) HandleGetIndex(c *gin.Context) {
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
