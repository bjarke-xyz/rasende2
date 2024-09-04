package app

import (
	"context"

	"github.com/bjarke-xyz/rasende2/internal/ai"
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/mail"
	"github.com/bjarke-xyz/rasende2/internal/news"
	"github.com/bjarke-xyz/rasende2/internal/repository"
)

func AppContext(cfg *config.Config) *core.AppContext {
	cache := repository.NewCacheService(repository.NewCacheRepo(cfg, true))
	mailService := mail.NewMail(cfg)

	appContext := &core.AppContext{
		Config: cfg,
		Infra: &core.AppInfra{
			Cache: cache,
			Mail:  mailService,
		},
		Deps: &core.AppDeps{},
	}

	rssRepository := repository.NewSqliteNews(appContext)
	rssSearch := news.NewRssSearch(cfg.SearchIndexPath)
	appContext.Deps.Service = news.NewRssService(appContext, rssRepository, rssSearch)
	appContext.Deps.AiClient = ai.NewOpenAIClient(appContext)

	return appContext
}

func Initialise(ctx context.Context, appContext *core.AppContext) {
	appContext.Deps.Service.Initialise(ctx)
}

func Dispose(appContext *core.AppContext) {
	appContext.Deps.Service.Dispose()
}
