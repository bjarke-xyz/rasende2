// Package server assembles the application's HTTP handler.
//
// It lives outside cmd/web so that the tests can exercise the same router the
// binary serves, rather than a reconstruction of it that is free to drift.
package server

import (
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/api"
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/session"
	"github.com/bjarke-xyz/rasende2/internal/web"
)

// New builds the HTTP handler: the routes, wrapped in the middleware stack.
//
// There is no CORS layer. The pages are server-rendered and same-origin, and the
// /api endpoints are called by cron with a key, not by a browser — so the
// Access-Control-Allow-Origin: * that used to go out on every response was
// answering a question nobody asked.
func New(appContext *core.AppContext) (http.Handler, error) {
	mux := http.NewServeMux()

	api.NewAPI(appContext).Route(mux)

	webHandlers, err := web.NewWeb(appContext)
	if err != nil {
		return nil, err
	}
	webHandlers.Route(mux)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	cfg := appContext.Config
	inProduction := cfg.AppEnv == config.AppEnvProduction

	// Outermost first. Recovery sits inside Logger so that a panicking request is
	// still logged, with the 500 it ended up returning.
	return httpx.Chain(mux,
		httpx.Logger(inProduction),
		httpx.Recovery,
		session.Middleware(session.NewStore(cfg.CookieSecret)),
	), nil
}
