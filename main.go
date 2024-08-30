package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/jobs"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/bjarke-xyz/rasende2-api/rss"
	"github.com/bjarke-xyz/rasende2-api/web/handlers"
	"github.com/bjarke-xyz/rasende2-api/web/renderer"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed web/static/*
var static embed.FS

//go:generate npx tailwindcss build -i web/static/css/style.css -o web/static/css/tailwind.css -m

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

	cacheRepo := pkg.NewCacheRepo(cfg)
	cacheService := pkg.NewCacheService(cacheRepo)

	ctx := &pkg.AppContext{
		Config:     cfg,
		JobManager: *jobs.NewJobManager(),
		Cache:      cacheService,
	}

	rssRepository := rss.NewRssRepository(ctx)
	rssSearch := rss.NewRssSearch(cfg.SearchIndexPath)
	rssService := rss.NewRssService(ctx, rssRepository, rssSearch)

	indexCreated, err := rssSearch.OpenAndCreateIndexIfNotExists()
	if err != nil {
		log.Printf("failed to open/create index: %v", err)
	}
	if indexCreated {
		go rssService.AddMissingItemsToSearchIndexAndLogError(context.Background(), nil)
	}
	defer rssSearch.CloseIndex()

	go func() {
		err := rssService.RefreshMetrics()
		if err != nil {
			log.Printf("error refreshing metrics: %v", err)
		}
	}()

	openAiClient := ai.NewOpenAIClient(ctx)

	defer ctx.JobManager.Stop()
	ctx.JobManager.Cron("1 * * * *", rss.JobIdentifierIngestion, func() error {
		job := rss.NewIngestionJob(rssService)
		return job.ExecuteJob()
	}, false)
	go ctx.JobManager.Start()

	runMetricsServer()

	rssHttpHandlers := rss.NewHttpHandlers(ctx, rssService, openAiClient, rssSearch)
	webHandlers := handlers.NewWebHandlers(ctx, rssService, openAiClient, rssSearch)

	r := ginRouter(cfg)
	// r.POST("/migrate", rssHttpHandlers.HandleMigrate(cfg.JobKey))
	r.GET("/api/search", rssHttpHandlers.HandleSearch)
	r.GET("/api/charts", rssHttpHandlers.HandleCharts)
	r.GET("/api/highlighted-fake-news", rssHttpHandlers.GetHighlightedFakeNews)
	r.GET("api/fake-news-article", rssHttpHandlers.GetFakeNewsArticle)
	r.POST("/api/set-highlight", rssHttpHandlers.SetHighlightedFakeNews)
	r.POST("/api/reset-content", rssHttpHandlers.ResetFakeNewsContent)
	r.POST("/api/vote-fake-news", rssHttpHandlers.HandleArticleVote)
	r.GET("/api/generate-titles", rssHttpHandlers.HandleGenerateTitles)
	r.GET("/api/generate-content", rssHttpHandlers.HandleGenerateArticleContent)
	r.GET("/api/sites", rssHttpHandlers.HandleSites)
	r.POST("/api/job", rssHttpHandlers.RunJob(cfg.JobKey))
	r.POST("/api/backup-db", rssHttpHandlers.BackupDb(cfg.JobKey))
	r.POST("/api/admin/rebuild-index", rssHttpHandlers.RebuildIndex(cfg.JobKey))
	r.POST("/api/admin/auto-generate-fake-news", rssHttpHandlers.AutoGenerateFakeNews(cfg.JobKey))
	r.POST("/api/admin/clean-fake-news", rssHttpHandlers.CleanUpFakeNews(cfg.JobKey))

	staticFiles(r, static)
	r.GET("/", webHandlers.HandleGetIndex)
	r.GET("/search", webHandlers.HandleGetSearch)
	r.POST("/search", webHandlers.HandlePostSearch)
	r.GET("/fake-news", webHandlers.HandleGetFakeNews)
	r.GET("/fake-news/:slug", webHandlers.HandleGetFakeNewsArticle)
	r.POST("/fake-news/:slug", webHandlers.HandleGetFakeNewsArticle)
	r.GET("/title-generator", webHandlers.HandleGetTitleGenerator)
	r.GET("/generate-titles", webHandlers.HandleGetSseTitles)
	r.GET("/generate-titles-sse", webHandlers.HandleGetTitleGeneratorSse)
	r.GET("/article-generator", webHandlers.HandleGetArticleGenerator)
	r.GET("/generate-article", webHandlers.HandleGetSseArticleContent)
	r.POST("/publish-fake-news", webHandlers.HandlePostPublishFakeNews)
	r.POST("/vote-article", webHandlers.HandlePostArticleVote)
	r.GET("/login", webHandlers.HandleGetLogin)
	r.POST("/login", webHandlers.HandlePostLogin)

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

func staticFiles(r *gin.Engine, staticFs fs.FS) {
	staticWeb, err := fs.Sub(staticFs, "web/static")
	if err != nil {
		log.Printf("failed to get fs sub for static: %v", err)
	}
	httpFsStaticWeb := http.FS(staticWeb)
	r.Use(staticCacheMiddleware())
	r.StaticFS("/static", httpFsStaticWeb)
	r.StaticFileFS("/favicon.ico", "./favicon.ico", httpFsStaticWeb)
	r.StaticFileFS("/favicon-16x16.png", "./favicon-16x16.png", httpFsStaticWeb)
	r.StaticFileFS("/favicon-32x32.png", "./favicon-32x32.png", httpFsStaticWeb)
	r.StaticFileFS("/apple-touch-icon.png", "./apple-touch-icon.png", httpFsStaticWeb)
	r.StaticFileFS("/site.webmanifest", "./site.webmanifest", httpFsStaticWeb)

}

func staticCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/static/js") || strings.HasPrefix(path, "/static/css") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	}
}
