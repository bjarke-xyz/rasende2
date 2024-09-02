package main

import (
	"log"
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/ai"
	"github.com/bjarke-xyz/rasende2/internal/api"
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/mail"
	"github.com/bjarke-xyz/rasende2/internal/news"
	"github.com/bjarke-xyz/rasende2/internal/repository"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/web"
	"github.com/bjarke-xyz/rasende2/internal/web/renderer"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.NewConfig()
	if err != nil {
		log.Printf("failed to load config: %v", err)
	}

	dbConn, err := db.Open(cfg)
	if err != nil {
		log.Printf("error opening db: %v", err)
	}
	if dbConn != nil {
		err = db.Migrate("up", dbConn.DB)
		if err != nil {
			log.Printf("failed to migrate: %v", err)
		}
	}

	cache := repository.NewCacheService(repository.NewCacheRepo(cfg, true))
	mailService := mail.NewMail(cfg)

	ctx := &core.AppContext{
		Config: cfg,
		Cache:  cache,
		Mail:   mailService,
	}

	rssRepository := repository.NewSqliteNews(ctx)
	rssSearch := news.NewRssSearch(cfg.SearchIndexPath)
	rssService := news.NewRssService(ctx, rssRepository, rssSearch)
	openAiClient := ai.NewOpenAIClient(ctx)

	rssService.Initialise()
	defer rssService.Dispose()

	runMetricsServer()

	apiHandlers := api.NewAPI(ctx, rssService, openAiClient, rssSearch)
	webHandlers := web.NewWeb(ctx, rssService, openAiClient, rssSearch)
	r := ginRouter(cfg)
	apiHandlers.Route(r)
	webHandlers.Route(r)

	log.Printf("Listening on http://localhost:%s", cfg.Port)
	r.Run()

}

func ginRouter(cfg *config.Config) *gin.Engine {
	if cfg.AppEnv == config.AppEnvProduction {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()
	store := cookie.NewStore([]byte(cfg.CookieSecret))
	r.Use(sessions.Sessions("mysession", store))
	r.Use(cors.Default())
	r.SetTrustedProxies(nil)
	if cfg.AppEnv == config.AppEnvProduction {
		r.TrustedPlatform = gin.PlatformCloudflare
	}
	ginHtmlRenderer := r.HTMLRender
	r.HTMLRender = &renderer.HTMLTemplRenderer{FallbackHtmlRenderer: ginHtmlRenderer}
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})
	return r
}

func runMetricsServer() {
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":9091", mux)
	}()
}
