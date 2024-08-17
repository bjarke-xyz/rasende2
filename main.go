package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/jobs"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/bjarke-xyz/rasende2-api/rss"
	"github.com/bjarke-xyz/rasende2-api/web/handlers"
	"github.com/bjarke-xyz/rasende2-api/web/renderer"
	"github.com/gin-contrib/cors"
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
	r.POST("/api/set-highlight", rssHttpHandlers.SetHighlightedFakeNews)
	r.GET("/api/generate-titles", rssHttpHandlers.HandleGenerateTitles)
	r.GET("/api/generate-content", rssHttpHandlers.HandleGenerateArticleContent)
	r.GET("/api/sites", rssHttpHandlers.HandleSites)
	r.POST("/api/job", rssHttpHandlers.RunJob(cfg.JobKey))
	r.POST("/api/backup-db", rssHttpHandlers.BackupDb(cfg.JobKey))
	r.POST("/api/admin/rebuild-index", rssHttpHandlers.RebuildIndex(cfg.JobKey))
	r.POST("/api/admin/auto-generate-fake-news", rssHttpHandlers.AutoGenerateFakeNews(cfg.JobKey))

	r.GET("/", webHandlers.IndexHandler)

	staticWeb, err := fs.Sub(static, "web/static")
	if err != nil {
		log.Printf("failed to get fs sub for static: %v", err)
	}
	r.StaticFS("/static", http.FS(staticWeb))

	log.Printf("Listening on http://localhost:%s", cfg.Port)
	r.Run()

}

func ginRouter(cfg *config.Config) *gin.Engine {
	if cfg.AppEnv == config.AppEnvProduction {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()
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
