package web

import (
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/gin-gonic/gin"
)

func (w *web) HandleGetIndex(c *gin.Context) {
	l := LangOf(c)
	base := w.getBaseModel(c, l.T("page.index"))
	base.ShowCredit = true
	indexModel := components.IndexModel{Base: base}
	ctx := c.Request.Context()
	indexPageData, err := w.appContext.Deps.Service.GetIndexPageData(ctx, l)
	if err != nil {
		w.renderError(c, http.StatusInternalServerError, err)
		return
	}
	indexModel.SearchResults = *indexPageData.SearchResult
	indexModel.ChartsResult = *indexPageData.ChartsResult
	w.renderer.Page(c, http.StatusOK, "index", indexModel.Base, indexModel)
}
