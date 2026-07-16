package app

import (
	"context"

	"github.com/bjarke-xyz/rasende2/internal/ai"
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/news"
	"github.com/bjarke-xyz/rasende2/internal/repository"
)

func AppContext(cfg *config.Config) *core.AppContext {

	appContext := &core.AppContext{
		Config: cfg,
		Deps:   &core.AppDeps{},
	}

	rssRepository := repository.NewSqliteNews(appContext)
	rssSearch := news.NewRssSearch(appContext, rssRepository)
	appContext.Deps.Service = news.NewRssService(appContext, rssRepository, rssSearch)
	appContext.Deps.AiClient = ai.NewLLMClient(appContext)

	return appContext
}

func Initialise(ctx context.Context, appContext *core.AppContext) {
	appContext.Deps.Service.Initialise(ctx)
}

func Dispose(appContext *core.AppContext) {
	appContext.Deps.Service.Dispose()
}
