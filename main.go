package main

import (
	"log"
	"net/http"

	"github.com/bjarke-xyz/rasende2-api/ai"
	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/jobs"
	"github.com/bjarke-xyz/rasende2-api/pkg"
	"github.com/bjarke-xyz/rasende2-api/rss"
	"github.com/gin-contrib/cors"
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

	context := &pkg.AppContext{
		Config:     cfg,
		JobManager: *jobs.NewJobManager(),
	}

	rssRepository := rss.NewRssRepository(context)
	rssSearch := rss.NewRssSearch(cfg.SearchIndexPath)
	rssService := rss.NewRssService(context, rssRepository, rssSearch)

	err = rssSearch.CreateIndexIfNotExists()
	if err != nil {
		log.Printf("failed to create index: %v", err)
	}

	openAiClient := ai.NewOpenAIClient(context)

	defer context.JobManager.Stop()
	context.JobManager.Cron("1 * * * *", rss.JobIdentifierIngestion, func() error {
		job := rss.NewIngestionJob(rssService)
		return job.ExecuteJob()
	}, false)
	go context.JobManager.Start()

	runMetricsServer()

	rssHttpHandlers := rss.NewHttpHandlers(context, rssService, openAiClient, rssSearch)

	r := ginRouter(cfg)
	r.POST("/migrate", rssHttpHandlers.HandleMigrate(cfg.JobKey))
	r.GET("/search", rssHttpHandlers.HandleSearch)
	r.GET("/charts", rssHttpHandlers.HandleCharts)
	r.GET("/generate-titles", rssHttpHandlers.HandleGenerateTitles)
	r.GET("/generate-content", rssHttpHandlers.HandleGenerateArticleContent)
	r.GET("/sites", rssHttpHandlers.HandleSites)
	r.POST("/job", rssHttpHandlers.RunJob(cfg.JobKey))
	r.POST("/backup-db", rssHttpHandlers.BackupDb(cfg.JobKey))

	r.Run()

}

func ginRouter(cfg *config.Config) *gin.Engine {
	if cfg.AppEnv == config.AppEnvProduction {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()
	r.Use(cors.Default())
	if cfg.AppEnv == config.AppEnvProduction {
		r.TrustedPlatform = gin.PlatformCloudflare
		r.SetTrustedProxies(nil)
	}
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
