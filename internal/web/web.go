package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/session"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
)

//go:embed static/*
var static embed.FS

type web struct {
	appContext *core.AppContext
	renderer   *Renderer
}

func NewWeb(appContext *core.AppContext) (*web, error) {
	renderer, err := NewRenderer(appContext.Config)
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return &web{
		appContext: appContext,
		renderer:   renderer,
	}, nil
}

// renderError renders the error page, wrapped in the layout for a normal
// request and bare for an htmx one.
func (h *web) renderError(w http.ResponseWriter, r *http.Request, status int, err error) {
	l := LangOf(r)
	base := h.getBaseModel(w, r, l.T("page.error"))
	h.renderer.Page(w, r, status, "error", base, components.ErrorModel{Base: base, Err: err, Unknown: l.T("error.unknown")})
}

// renderErrorFragment renders the error page without the layout, for endpoints
// whose response is always swapped into an existing page. htmx's SSE extension
// does not set the HX-Request header, so those handlers cannot rely on
// getBaseModel to work it out.
func (h *web) renderErrorFragment(w http.ResponseWriter, r *http.Request, status int, err error) {
	h.renderer.Partial(w, r, status, "error", components.ErrorModel{Err: err})
}

// langContextKey holds the request's edition, put there by langMiddleware. Every
// handler and every template render reads it from here rather than re-deriving
// it from the path.
type langContextKey struct{}

// LangOf returns the edition being served. Handlers only ever run inside a
// language group, so a miss means the route was registered outside the loop in
// Route — fall back rather than panic, but the page will be in the wrong
// language, which is loud enough.
func LangOf(r *http.Request) lang.Lang {
	if l, ok := r.Context().Value(langContextKey{}).(lang.Lang); ok {
		return l
	}
	return lang.MustGet(lang.Default)
}

// editionRoot is the front page of the edition being served, and the fallback
// for any redirect that would otherwise land on "/" and bounce the visitor back
// through language negotiation.
//
// No trailing slash: the routes are registered as /da, so /da/ would only 301
// its way here anyway, and pointing redirects at it would cost every one of them
// an extra hop. The layout's <base href> keeps its slash — that is a different
// thing, and relative paths need it to resolve inside the edition.
func editionRoot(r *http.Request) string {
	return "/" + string(LangOf(r).Code)
}

func langMiddleware(l lang.Lang) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), langContextKey{}, l)))
		})
	}
}

// Route registers the two editions under literal /da and /en prefixes.
//
// A single "/{lang}" pattern would also work, but it would turn every unknown
// root path into a language attempt, so /robots.txt would render the index page
// as language "robots.txt" instead of 404ing. Literal prefixes keep unknown
// paths unknown.
//
// The static assets and the legacy redirects live outside the loop: they are the
// same in every language, and registering them per edition would serve
// /da/favicon.ico.
func (h *web) Route(mux *http.ServeMux) {
	staticFiles(mux, static)

	for _, l := range lang.All {
		h.routes(mux, l)
	}

	// The OIDC callback is edition-agnostic (one registered redirect URI); the
	// edition is carried in the return path stashed during login.
	mux.HandleFunc("GET /auth/callback", h.HandleGetAuthCallback)

	h.routeRoot(mux)
}

// routes registers one edition. The language appears only in the pattern prefix;
// langMiddleware is what puts the edition itself on the request.
func (h *web) routes(mux *http.ServeMux, l lang.Lang) {
	prefix := "/" + string(l.Code)
	withLang := langMiddleware(l)

	handle := func(method, path string, fn http.HandlerFunc) {
		mux.Handle(method+" "+prefix+path, withLang(fn))
	}

	// "" rather than "/", so the route is /da and not /da/: the header's
	// current-link check compares the raw request path. It also matters to
	// ServeMux, where a trailing slash would make this a subtree pattern that
	// swallows every unknown path beneath it.
	//
	// A GET pattern serves HEAD too, so the index needs no separate registration.
	handle(http.MethodGet, "", h.HandleGetIndex)
	handle(http.MethodGet, "/search", h.HandleGetSearch)
	handle(http.MethodPost, "/search", h.HandlePostSearch)
	handle(http.MethodGet, "/fake-news", h.HandleGetFakeNews)
	handle(http.MethodGet, "/fake-news/{slug}", h.HandleGetFakeNewsArticle)
	handle(http.MethodPost, "/fake-news/{slug}", h.HandleGetFakeNewsArticle)
	handle(http.MethodGet, "/title-generator", h.HandleGetTitleGenerator)
	handle(http.MethodGet, "/generate-titles", h.HandleGetSseTitles)
	handle(http.MethodGet, "/generate-titles-sse", h.HandleGetTitleGeneratorSse)
	handle(http.MethodGet, "/article-generator", h.HandleGetArticleGenerator)
	handle(http.MethodGet, "/generate-article", h.HandleGetSseArticleContent)
	handle(http.MethodPost, "/publish-fake-news", h.HandlePostPublishFakeNews)
	handle(http.MethodPost, "/vote-article", h.HandlePostArticleVote)
	handle(http.MethodPost, "/reset-article-content", h.HandlePostResetContent)
	handle(http.MethodGet, "/login", h.HandleGetLogin)
	handle(http.MethodPost, "/logout", h.HandlePostLogout)

	// /da/ 301s to /da. gin redirected the trailing slash away for free; ServeMux
	// would 404 it, and it is a URL people have.
	mux.Handle("GET "+prefix+"/{$}", http.RedirectHandler(prefix, http.StatusMovedPermanently))
}

// legacyPaths are the pages that existed before the editions did. Fake news
// articles in particular have been shared, so their links have to keep working;
// they all belong to the Danish edition, which is the only one that existed.
//
// Only GETs. A 301 on a POST makes the browser re-issue it as a GET, and nothing
// outside our own pages posts to these anyway, so the old POST routes simply go
// away.
var legacyPaths = []string{
	"/search",
	"/fake-news",
	"/fake-news/{slug}",
	"/title-generator",
	"/article-generator",
	"/login",
}

func (h *web) routeRoot(mux *http.ServeMux) {
	// "/{$}" and not "/": in ServeMux a bare "/" is a subtree pattern that matches
	// everything not matched elsewhere, which would send /robots.txt to the
	// default edition instead of 404ing it.
	mux.HandleFunc("GET /{$}", h.HandleGetRoot)

	for _, path := range legacyPaths {
		mux.HandleFunc("GET "+path, redirectToDefaultEdition)
	}
}

// HandleGetRoot sends a visitor to the default edition.
//
// Accept-Language is deliberately ignored. Plenty of Danes run their browser in
// English, so the header says more about how someone set up their laptop than
// about which edition they came for — and this is a Danish site by origin. They
// get Danish, and the switcher is one click away.
func (h *web) HandleGetRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/"+string(lang.Default), http.StatusFound)
}

func redirectToDefaultEdition(w http.ResponseWriter, r *http.Request) {
	target := "/" + string(lang.Default) + r.URL.Path
	if raw := r.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func (h *web) getBaseModel(w http.ResponseWriter, r *http.Request, title string) components.BaseViewModel {
	var unixBuildTime int64 = 0
	if h.appContext.Config.BuildTime != nil {
		unixBuildTime = h.appContext.Config.BuildTime.Unix()
	} else {
		unixBuildTime = time.Now().Unix()
	}
	hxRequest := r.Header.Get("HX-Request")
	includeLayout := hxRequest == "" || hxRequest == "false"
	_, loggedIn := session.UserID(r)
	l := LangOf(r)
	model := components.BaseViewModel{
		Path:            r.URL.Path,
		Lang:            string(l.Code),
		Editions:        editionsFor(r, l),
		UnixBuildTime:   unixBuildTime,
		Title:           title,
		IncludeLayout:   includeLayout,
		FlashInfo:       session.Flashes(w, r, core.FlashTypeInfo),
		FlashWarn:       session.Flashes(w, r, core.FlashTypeWarn),
		FlashError:      session.Flashes(w, r, core.FlashTypeError),
		IsAnonymousUser: !loggedIn,
		IsAdmin:         session.IsAdmin(r),
	}
	return model
}

// editionsFor builds the language switcher: the other editions, each linking to
// the same page it is currently on, so switching language does not also throw
// away where the visitor was.
func editionsFor(r *http.Request, current lang.Lang) []components.Edition {
	rest := strings.TrimPrefix(r.URL.Path, "/"+string(current.Code))
	editions := make([]components.Edition, 0, len(lang.All)-1)
	for _, l := range lang.All {
		if l.Code == current.Code {
			continue
		}
		path := "/" + string(l.Code) + rest
		if raw := r.URL.RawQuery; raw != "" {
			path += "?" + raw
		}
		editions = append(editions, components.Edition{Path: path, Text: l.Endonym})
	}
	return editions
}

var staticFileNames = []string{
	"favicon.ico",
	"favicon-16x16.png",
	"favicon-32x32.png",
	"apple-touch-icon.png",
	"site.webmanifest",
}

func staticFiles(mux *http.ServeMux, staticFs fs.FS) {
	staticWeb, err := fs.Sub(staticFs, "static")
	if err != nil {
		log.Printf("failed to get fs sub for static: %v", err)
		return
	}

	mux.Handle("GET /static/", staticCache(http.StripPrefix("/static/", http.FileServerFS(staticWeb))))

	// The browser asks for these at the root whatever page it is on, so they
	// cannot live under the language prefix.
	for _, name := range staticFileNames {
		mux.Handle("GET /"+name, serveFile(staticWeb, name))
	}
}

func serveFile(fsys fs.FS, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, fsys, name)
	})
}

// staticCache marks the fingerprinted assets as immutable. Only js and css carry
// a build-time query string, so only they are safe to cache forever.
func staticCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/static/js") || strings.HasPrefix(path, "/static/css") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		next.ServeHTTP(w, r)
	})
}
