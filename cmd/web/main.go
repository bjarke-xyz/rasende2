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

	"github.com/bjarke-xyz/rasende2/internal/app"
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/server"
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
		err = db.Migrate("up", dbConn)
		if err != nil {
			log.Printf("failed to migrate: %v", err)
		}
	}

	appContext := app.AppContext(cfg)
	app.Initialise(ctx, appContext)
	defer app.Dispose(appContext)

	runMetricsServer()

	srv, err := Server(appContext)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}
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

func Server(appContext *core.AppContext) (*http.Server, error) {
	handler, err := server.New(appContext)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", appContext.Config.Port),
		Handler: handler,

		// No WriteTimeout: it is a deadline on the whole response, and the
		// title/article generators stream for as long as the model takes.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, nil
}
