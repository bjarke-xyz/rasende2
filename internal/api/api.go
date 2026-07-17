package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/pkg"
)

type api struct {
	appContext *core.AppContext
}

func NewAPI(appContext *core.AppContext) *api {
	return &api{
		appContext: appContext,
	}
}

func (a *api) Route(mux *http.ServeMux) {
	handle := func(path string, fn http.HandlerFunc) {
		mux.Handle("POST /api"+path, a.requireJobKey(fn))
	}
	handle("/job", a.RunJob)
	handle("/admin/rebuild-index", a.RebuildIndex)
	handle("/admin/auto-generate-fake-news", a.AutoGenerateFakeNews)
	handle("/admin/clean-fake-news", a.CleanUpFakeNews)
}

// requireJobKey guards the endpoints the cron calls. They are the only way into
// the app that is not a browser, and they do expensive, destructive things.
func (a *api) requireJobKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := a.appContext.Config.JobKey

		// An unset JOB_KEY used to mean a request with no Authorization header
		// matched it, which left these open to anyone who guessed the path. No key
		// configured now means no access.
		if key == "" {
			slog.Error("api: JOB_KEY is not set; refusing request", "path", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		given := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(given), []byte(key)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) RunJob(w http.ResponseWriter, r *http.Request) {
	fireAndForget := r.URL.Query().Get("fireAndForget") == "true"
	//using context.Background to not cancel, if this method times out
	ctx := context.Background()

	if fireAndForget {
		go a.appContext.Deps.Service.FetchAndSaveNewItems(ctx)
	} else {
		a.appContext.Deps.Service.FetchAndSaveNewItems(ctx)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *api) CleanUpFakeNews(w http.ResponseWriter, r *http.Request) {
	fireAndForget := r.URL.Query().Get("fireAndForget") == "true"
	if fireAndForget {
		go a.appContext.Deps.Service.CleanUpFakeNewsAndLogError(context.Background())
	} else {
		ctx := r.Context()
		err := a.appContext.Deps.Service.CleanUpFakeNews(ctx)
		if err != nil {
			httpx.String(w, http.StatusInternalServerError, "fake news clean up failed: %v", err)
			return
		}
		slog.Info("fake news clean up success")
	}
	w.WriteHeader(http.StatusOK)
}

// RebuildIndex discards rss_items_fts and reindexes every item. Ordinary indexing
// happens transactionally on insert, so this is only needed after an analyzer change.
func (a *api) RebuildIndex(w http.ResponseWriter, r *http.Request) {
	go a.appContext.Deps.Service.RebuildSearchIndexAndLogError(context.Background())
	w.WriteHeader(http.StatusOK)
}

var noAutoGenerateSites map[int]any = map[int]any{8: struct{}{} /* DR */, 19: struct{}{} /* TV2 */}

func (a *api) AutoGenerateFakeNews(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	// The cron generates for every edition, not just one, so it samples across
	// all of them. Each site carries its own language, and that is what decides
	// the language the article comes back in.
	allSites := make([]core.NewsSite, 0)
	for _, l := range lang.All {
		sites, err := a.appContext.Deps.Service.GetSiteInfos(ctx, l)
		if err != nil {
			slog.Error("getting site infos failed", "error", err)
			httpx.JSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		allSites = append(allSites, sites...)
	}
	latestFakeNews, err := a.appContext.Deps.Service.GetRecentFakeNews(ctx, 3, nil)
	if err != nil {
		slog.Error("getting recent fake news failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	latestFakeNewsSites := make(map[int]any, len(latestFakeNews))
	for _, fn := range latestFakeNews {
		latestFakeNewsSites[fn.SiteId] = struct{}{}
	}
	sites := make([]core.NewsSite, 0)
	for _, site := range allSites {
		if site.Disabled {
			continue
		}
		_, isInLatest := latestFakeNewsSites[site.Id]
		if isInLatest {
			continue
		}
		_, isNoAutoGenerateSite := noAutoGenerateSites[site.Id]
		if isNoAutoGenerateSite {
			continue
		}
		sites = append(sites, site)
	}
	if len(sites) == 0 {
		httpx.JSON(w, http.StatusInternalServerError, "sites list was empty")
		return
	}
	site := sites[rand.IntN(len(sites))]
	recentArticleTitles, err := a.appContext.Deps.Service.GetRecentTitles(ctx, site, 10, true)
	if err != nil {
		slog.Error("getting recent article titles failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	var temperature float32 = 1
	var generatedTitleCount = 30
	generatedArticleTitles, err := a.appContext.Deps.AiClient.GenerateArticleTitlesList(ctx, site, recentArticleTitles, generatedTitleCount, temperature)
	if err != nil {
		slog.Error("getting generated article titles failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Debug("generated titles", "titles", strings.Join(generatedArticleTitles, ", "))
	selectedTitle, err := a.appContext.Deps.AiClient.SelectBestArticleTitle(ctx, site, generatedArticleTitles)
	if err != nil {
		slog.Error("selecting best article title failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Debug("selected title", "title", selectedTitle)
	externalId := pkg.NewID()
	err = a.appContext.Deps.Service.CreateFakeNews(ctx, site.Id, selectedTitle, externalId)
	if err != nil {
		slog.Error("creating fake news failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	articleImgPromise := pkg.NewPromise(func() (string, error) {
		imgUrl, err := a.appContext.Deps.AiClient.GenerateImage(ctx, site, selectedTitle, true)
		if err != nil {
			slog.Error("making fake news img failed", "error", err)
		}
		if imgUrl != "" {
			a.appContext.Deps.Service.SetFakeNewsImgUrl(ctx, site.Id, selectedTitle, imgUrl)
		}
		return imgUrl, err
	})

	articleContent, err := a.appContext.Deps.AiClient.GenerateArticleContentStr(ctx, site, selectedTitle, temperature)
	if err != nil {
		slog.Error("generating article content failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	err = a.appContext.Deps.Service.UpdateFakeNews(ctx, site.Id, selectedTitle, articleContent)
	if err != nil {
		slog.Error("updating fake news failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	slog.Debug("waiting for img")
	articleImgPromise.Get()
	slog.Debug("img done")

	err = a.appContext.Deps.Service.SetFakeNewsHighlighted(ctx, site.Id, selectedTitle, true)
	if err != nil {
		slog.Error("setting highlighted failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	createdFakeNews, err := a.appContext.Deps.Service.GetFakeNews(ctx, externalId)
	if err != nil {
		slog.Error("getting fake news failed", "error", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if createdFakeNews == nil {
		slog.Warn("fake news was nil")
		httpx.JSON(w, http.StatusInternalServerError, "fake new was nil")
		return
	}

	httpx.JSON(w, http.StatusOK, *createdFakeNews)
}
