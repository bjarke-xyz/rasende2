package web

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/web/auth"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/gin-gonic/gin"
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
func (w *web) renderError(c *gin.Context, status int, err error) {
	l := LangOf(c)
	base := w.getBaseModel(c, l.T("page.error"))
	w.renderer.Page(c, status, "error", base, components.ErrorModel{Base: base, Err: err, Unknown: l.T("error.unknown")})
}

// renderErrorFragment renders the error page without the layout, for endpoints
// whose response is always swapped into an existing page. htmx's SSE extension
// does not set the HX-Request header, so those handlers cannot rely on
// getBaseModel to work it out.
func (w *web) renderErrorFragment(c *gin.Context, status int, err error) {
	w.renderer.Partial(c, status, "error", components.ErrorModel{Err: err})
}

// langContextKey holds the request's edition, put there by langMiddleware. Every
// handler and every template render reads it from here rather than re-deriving
// it from the path.
const langContextKey = "lang"

// LangOf returns the edition being served. Handlers only ever run inside a
// language group, so a miss means the route was registered outside the loop in
// Route — fall back rather than panic, but the page will be in the wrong
// language, which is loud enough.
func LangOf(c *gin.Context) lang.Lang {
	if l, ok := c.Get(langContextKey); ok {
		if l, ok := l.(lang.Lang); ok {
			return l
		}
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
func editionRoot(c *gin.Context) string {
	return "/" + string(LangOf(c).Code)
}

func langMiddleware(l lang.Lang) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(langContextKey, l)
		c.Next()
	}
}

// Route registers the two editions under literal /da and /en prefixes.
//
// A single "/:lang" group would also work — gin does not object to it alongside
// /static and /api — but it would turn every unknown root path into a language
// attempt, so /robots.txt would render the index page as language "robots.txt"
// instead of 404ing. Literal prefixes keep unknown paths unknown.
//
// The static assets, /health and the legacy redirects live outside the loop:
// they are the same in every language, and registering them per edition would
// install the static cache middleware once per language and serve
// /da/favicon.ico.
func (w *web) Route(r *gin.Engine) {
	staticFiles(r, static)

	for _, l := range lang.All {
		g := r.Group("/"+string(l.Code), langMiddleware(l))
		w.routes(g)
	}

	w.routeRoot(r)
}

// routes registers one edition. The paths are relative to the group, so the
// group prefix is the only place the language appears.
func (w *web) routes(r *gin.RouterGroup) {
	// "" rather than "/", so the route is /da and not /da/: gin's joinPaths
	// appends the slash, and the header's current-link check compares the raw
	// request path.
	r.HEAD("", w.HandleGetIndex)
	r.GET("", w.HandleGetIndex)
	r.GET("/search", w.HandleGetSearch)
	r.POST("/search", w.HandlePostSearch)
	r.GET("/fake-news", w.HandleGetFakeNews)
	r.GET("/fake-news/:slug", w.HandleGetFakeNewsArticle)
	r.POST("/fake-news/:slug", w.HandleGetFakeNewsArticle)
	r.GET("/title-generator", w.HandleGetTitleGenerator)
	r.GET("/generate-titles", w.HandleGetSseTitles)
	r.GET("/generate-titles-sse", w.HandleGetTitleGeneratorSse)
	r.GET("/article-generator", w.HandleGetArticleGenerator)
	r.GET("/generate-article", w.HandleGetSseArticleContent)
	r.POST("/publish-fake-news", w.HandlePostPublishFakeNews)
	r.POST("/vote-article", w.HandlePostArticleVote)
	r.POST("/reset-article-content", w.HandlePostResetContent)
	r.GET("/login", w.HandleGetLogin)
	r.GET("/login-link", w.HandleGetLoginLink)
	r.POST("/login", w.HandlePostLogin)
	r.POST("/logout", w.HandlePostLogout)
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
	"/fake-news/:slug",
	"/title-generator",
	"/article-generator",
	"/login",
}

func (w *web) routeRoot(r *gin.Engine) {
	r.GET("/", w.HandleGetRoot)
	r.HEAD("/", w.HandleGetRoot)
	for _, path := range legacyPaths {
		r.GET(path, redirectToDefaultEdition)
	}
}

// HandleGetRoot sends a visitor to the default edition.
//
// Accept-Language is deliberately ignored. Plenty of Danes run their browser in
// English, so the header says more about how someone set up their laptop than
// about which edition they came for — and this is a Danish site by origin. They
// get Danish, and the switcher is one click away.
func (w *web) HandleGetRoot(c *gin.Context) {
	c.Redirect(http.StatusFound, "/"+string(lang.Default))
}

func redirectToDefaultEdition(c *gin.Context) {
	target := "/" + string(lang.Default) + c.Request.URL.Path
	if raw := c.Request.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	c.Redirect(http.StatusMovedPermanently, target)
}

func (w *web) getBaseModel(c *gin.Context, title string) components.BaseViewModel {
	var unixBuildTime int64 = 0
	if w.appContext.Config.BuildTime != nil {
		unixBuildTime = w.appContext.Config.BuildTime.Unix()
	} else {
		unixBuildTime = time.Now().Unix()
	}
	hxRequest := c.Request.Header.Get("HX-Request")
	includeLayout := hxRequest == "" || hxRequest == "false"
	userId, ok := auth.GetUserId(c)
	l := LangOf(c)
	model := components.BaseViewModel{
		Path:            c.Request.URL.Path,
		Lang:            string(l.Code),
		Editions:        editionsFor(c, l),
		UnixBuildTime:   unixBuildTime,
		Title:           title,
		IncludeLayout:   includeLayout,
		FlashInfo:       GetFlashes(c, core.FlashTypeInfo),
		FlashWarn:       GetFlashes(c, core.FlashTypeWarn),
		FlashError:      GetFlashes(c, core.FlashTypeError),
		UserId:          userId,
		IsAnonymousUser: !ok,
		IsAdmin:         auth.IsAdmin(c),
	}
	return model
}

// editionsFor builds the language switcher: the other editions, each linking to
// the same page it is currently on, so switching language does not also throw
// away where the visitor was.
func editionsFor(c *gin.Context, current lang.Lang) []components.Edition {
	rest := strings.TrimPrefix(c.Request.URL.Path, "/"+string(current.Code))
	editions := make([]components.Edition, 0, len(lang.All)-1)
	for _, l := range lang.All {
		if l.Code == current.Code {
			continue
		}
		path := "/" + string(l.Code) + rest
		if raw := c.Request.URL.RawQuery; raw != "" {
			path += "?" + raw
		}
		editions = append(editions, components.Edition{Path: path, Text: l.Endonym})
	}
	return editions
}

func staticFiles(r *gin.Engine, staticFs fs.FS) {
	staticWeb, err := fs.Sub(staticFs, "static")
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
