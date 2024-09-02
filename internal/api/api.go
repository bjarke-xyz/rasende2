package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/news"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type api struct {
	context  *core.AppContext
	service  *news.RssService
	aiClient core.AiClient
	search   *news.RssSearch
}

func NewAPI(context *core.AppContext, service *news.RssService, openaiClient core.AiClient, search *news.RssSearch) *api {
	return &api{
		context:  context,
		service:  service,
		aiClient: openaiClient,
		search:   search,
	}
}

func (a *api) Route(r *gin.Engine) {
	apiGroup := r.Group("/api")
	apiGroup.POST("/job", a.RunJob())
	apiGroup.POST("/backup-db", a.BackupDb())
	apiGroup.POST("/admin/rebuild-index", a.RebuildIndex())
	apiGroup.POST("/admin/auto-generate-fake-news", a.AutoGenerateFakeNews())
	apiGroup.POST("/admin/clean-fake-news", a.CleanUpFakeNews())
}

func (a *api) RunJob() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != a.context.Config.JobKey {
			c.AbortWithStatus(401)
			return
		}
		fireAndForget := c.Query("fireAndForget") == "true"
		if fireAndForget {
			go a.service.FetchAndSaveNewItems()
		} else {
			a.service.FetchAndSaveNewItems()
		}
		c.Status(http.StatusOK)
	}
}

func (a *api) BackupDb() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != a.context.Config.JobKey {
			c.AbortWithStatus(401)
			return
		}
		fireAndForget := c.Query("fireAndForget") == "true"
		if fireAndForget {
			go a.service.BackupDbAndLogError(context.Background())
		} else {
			ctx := c.Request.Context()
			err := a.service.BackupDbAndLogError(ctx)
			if err != nil {
				c.String(http.StatusInternalServerError, "backup failed: %v", err)
				return
			}
			log.Printf("backup success")
		}
		c.Status(http.StatusOK)
	}
}

func (a *api) CleanUpFakeNews() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != a.context.Config.JobKey {
			c.AbortWithStatus(401)
			return
		}
		fireAndForget := c.Query("fireAndForget") == "true"
		if fireAndForget {
			go a.service.CleanUpFakeNewsAndLogError(context.Background())
		} else {
			ctx := c.Request.Context()
			err := a.service.CleanUpFakeNews(ctx)
			if err != nil {
				c.String(http.StatusInternalServerError, "fake news clean up failed: %v", err)
				return
			}
			log.Printf("fake news clean up success")
		}
		c.Status(http.StatusOK)
	}
}

func (a *api) RebuildIndex() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != a.context.Config.JobKey {
			c.AbortWithStatus(401)
			return
		}
		var maxLookBack *time.Time
		maxLookBackStr := c.Query("maxLookBack")
		if maxLookBackStr != "" {
			_maxLookBack, err := time.Parse(time.RFC3339, maxLookBackStr)
			if err != nil {
				log.Printf("error parsing max lookback str %v: %v", maxLookBackStr, err)
				c.AbortWithError(http.StatusBadRequest, err)
				return
			}
			maxLookBack = &_maxLookBack
		}
		go a.service.AddMissingItemsToSearchIndexAndLogError(context.Background(), maxLookBack)
		c.Status(http.StatusOK)
	}
}

var noAutoGenerateSites map[int]any = map[int]any{8: struct{}{} /* DR */, 19: struct{}{} /* TV2 */}

func (a *api) AutoGenerateFakeNews() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != a.context.Config.JobKey {
			c.AbortWithStatus(401)
			return
		}
		ctx := context.Background()
		allSites, err := a.service.GetSiteInfos()
		if err != nil {
			log.Printf("error site infos: %v", err)
			c.JSON(500, err.Error())
			return
		}
		latestFakeNews, err := a.service.GetRecentFakeNews(3, nil)
		if err != nil {
			log.Printf("error getting recent fake news: %v", err)
			c.JSON(500, err.Error())
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
			c.JSON(500, "sites list was empty")
			return
		}
		site := lo.Sample(sites)
		recentArticleTitles, err := a.service.GetRecentTitles(ctx, site, 10, true)
		if err != nil {
			log.Printf("error getting recent article titles: %v", err)
			c.JSON(500, err.Error())
			return
		}
		var temperature float32 = 1
		var generatedTitleCount = 30
		generatedArticleTitles, err := a.aiClient.GenerateArticleTitlesList(ctx, site.Name, site.DescriptionEn, recentArticleTitles, generatedTitleCount, temperature)
		if err != nil {
			log.Printf("error getting generated article titles: %v", err)
			c.JSON(500, err.Error())
			return
		}
		log.Printf("generated titles: %v", strings.Join(generatedArticleTitles, ", "))
		selectedTitle, err := a.aiClient.SelectBestArticleTitle(ctx, site.Name, site.DescriptionEn, generatedArticleTitles)
		if err != nil {
			log.Printf("error selecting best article title: %v", err)
			c.JSON(500, err.Error())
			return
		}
		log.Printf("selected title: %v", selectedTitle)

		err = a.service.CreateFakeNews(site.Id, selectedTitle)
		if err != nil {
			log.Printf("error creating fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}

		articleImgPromise := pkg.NewPromise(func() (string, error) {
			imgUrl, err := a.aiClient.GenerateImage(ctx, site.Name, site.DescriptionEn, selectedTitle, true)
			if err != nil {
				log.Printf("error making fake news img: %v", err)
			}
			if imgUrl != "" {
				a.service.SetFakeNewsImgUrl(site.Id, selectedTitle, imgUrl)
			}
			return imgUrl, err
		})

		articleContent, err := a.aiClient.GenerateArticleContentStr(ctx, site.Name, site.DescriptionEn, selectedTitle, temperature)
		if err != nil {
			log.Printf("error generating article content: %v", err)
			c.JSON(500, err.Error())
			return
		}

		err = a.service.UpdateFakeNews(site.Id, selectedTitle, articleContent)
		if err != nil {
			log.Printf("error updating fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}

		log.Printf("waiting for img...")
		articleImgPromise.Get()
		log.Printf("img done!")

		err = a.service.SetFakeNewsHighlighted(site.Id, selectedTitle, true)
		if err != nil {
			log.Printf("error setting highlighted: %v", err)
			c.JSON(500, err.Error())
			return
		}

		createdFakeNews, err := a.service.GetFakeNews(site.Id, selectedTitle)
		if err != nil {
			log.Printf("error getting fake news: %v", err)
			c.JSON(500, err.Error())
			return
		}
		if createdFakeNews == nil {
			log.Printf("fake news was nil")
			c.JSON(500, "fake new was nil")
			return
		}

		c.JSON(200, *createdFakeNews)
	}
}
