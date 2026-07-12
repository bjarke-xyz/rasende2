package web

import (
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/web/components"
)

func (h *web) HandleGetIndex(w http.ResponseWriter, r *http.Request) {
	l := LangOf(r)
	base := h.getBaseModel(w, r, l.T("page.index"))
	base.ShowCredit = true
	indexModel := components.IndexModel{Base: base}
	ctx := r.Context()
	indexPageData, err := h.appContext.Deps.Service.GetIndexPageData(ctx, l)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	indexModel.SearchResults = *indexPageData.SearchResult
	indexModel.ChartsResult = *indexPageData.ChartsResult
	h.renderer.Page(w, r, http.StatusOK, "index", indexModel.Base, indexModel)
}
