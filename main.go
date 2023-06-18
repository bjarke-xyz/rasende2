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

	err = db.Migrate("up", cfg.ConnectionString())
	if err != nil {
		log.Printf("failed to migrate: %v", err)
	}

	redis, err := db.NewRedisCache(cfg)
	if err != nil {
		log.Printf("failed to create redis: %v", err)
	}
	context := &pkg.AppContext{
		Cache:      redis,
		Config:     cfg,
		JobManager: *jobs.NewJobManager(),
	}

	rssRepository := rss.NewRssRepository(context)
	rssService := rss.NewRssService(context, rssRepository)

	openAiClient := ai.NewOpenAIClient(context)

	defer context.JobManager.Stop()
	context.JobManager.Cron("1 * * * *", rss.JobIdentifierIngestion, func() error {
		job := rss.NewIngestionJob(rssService)
		return job.ExecuteJob()
	}, false)
	go context.JobManager.Start()

	runMetricsServer()

	rssHttpHandlers := rss.NewHttpHandlers(context, rssService, openAiClient)

	r := ginRouter(cfg)
	r.GET("/search", rssHttpHandlers.HandleSearch)
	r.GET("/charts", rssHttpHandlers.HandleCharts)
	r.GET("/generate-titles", rssHttpHandlers.HandleGenerateTitles)
	r.GET("/sites", rssHttpHandlers.HandleSites)
	r.POST("/job", rssHttpHandlers.RunJob(cfg.JobKey))

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
