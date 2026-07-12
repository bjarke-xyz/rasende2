package api

import (
	"context"
	"crypto/subtle"
	"log"
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
			log.Printf("api: JOB_KEY is not set; refusing %v", r.URL.Path)
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
		log.Printf("fake news clean up success")
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
			log.Printf("error site infos: %v", err)
			httpx.JSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		allSites = append(allSites, sites...)
	}
	latestFakeNews, err := a.appContext.Deps.Service.GetRecentFakeNews(ctx, 3, nil)
	if err != nil {
		log.Printf("error getting recent fake news: %v", err)
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
		log.Printf("error getting recent article titles: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	var temperature float32 = 1
	var generatedTitleCount = 30
	generatedArticleTitles, err := a.appContext.Deps.AiClient.GenerateArticleTitlesList(ctx, site, recentArticleTitles, generatedTitleCount, temperature)
	if err != nil {
		log.Printf("error getting generated article titles: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("generated titles: %v", strings.Join(generatedArticleTitles, ", "))
	selectedTitle, err := a.appContext.Deps.AiClient.SelectBestArticleTitle(ctx, site, generatedArticleTitles)
	if err != nil {
		log.Printf("error selecting best article title: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("selected title: %v", selectedTitle)
	externalId := pkg.NewID()
	err = a.appContext.Deps.Service.CreateFakeNews(ctx, site.Id, selectedTitle, externalId)
	if err != nil {
		log.Printf("error creating fake news: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	articleImgPromise := pkg.NewPromise(func() (string, error) {
		imgUrl, err := a.appContext.Deps.AiClient.GenerateImage(ctx, site, selectedTitle, true)
		if err != nil {
			log.Printf("error making fake news img: %v", err)
		}
		if imgUrl != "" {
			a.appContext.Deps.Service.SetFakeNewsImgUrl(ctx, site.Id, selectedTitle, imgUrl)
		}
		return imgUrl, err
	})

	articleContent, err := a.appContext.Deps.AiClient.GenerateArticleContentStr(ctx, site, selectedTitle, temperature)
	if err != nil {
		log.Printf("error generating article content: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	err = a.appContext.Deps.Service.UpdateFakeNews(ctx, site.Id, selectedTitle, articleContent)
	if err != nil {
		log.Printf("error updating fake news: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("waiting for img...")
	articleImgPromise.Get()
	log.Printf("img done!")

	err = a.appContext.Deps.Service.SetFakeNewsHighlighted(ctx, site.Id, selectedTitle, true)
	if err != nil {
		log.Printf("error setting highlighted: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	createdFakeNews, err := a.appContext.Deps.Service.GetFakeNews(ctx, externalId)
	if err != nil {
		log.Printf("error getting fake news: %v", err)
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if createdFakeNews == nil {
		log.Printf("fake news was nil")
		httpx.JSON(w, http.StatusInternalServerError, "fake new was nil")
		return
	}

	httpx.JSON(w, http.StatusOK, *createdFakeNews)
}
