package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/api"
	"github.com/bjarke-xyz/rasende2/internal/app"
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
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

	// Create a context that will be canceled when we receive a shutdown signal
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel to listen for OS signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

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

	appContext := app.AppContext(cfg)
	app.Initialise(ctx, appContext)
	defer app.Dispose(appContext)

	runMetricsServer()

	srv := Server(appContext)
	go func() {
		log.Printf("Starting server on http://localhost%v", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()

	// Block until we receive a signal
	<-stop
	log.Println("Shutting down server...")

	// Cancel the context to signal all handlers that the server is shutting down
	cancel()

	// Create a context with a timeout for the server shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Shutdown the server gracefully
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server Shutdown Failed:%+v", err)
	}
	log.Println("Server exited properly")
}

func runMetricsServer() {
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":9091", mux)
	}()
}

func Server(appContext *core.AppContext) *http.Server {
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", appContext.Config.Port),
		Handler: routes(appContext),
	}
}

func routes(appContext *core.AppContext) http.Handler {
	r := ginRouter(appContext.Config)

	apiHandlers := api.NewAPI(appContext)
	apiHandlers.Route(r)

	webHandlers := web.NewWeb(appContext)
	webHandlers.Route(r)
	return r
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
