package web

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/web/auth"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/sashabaranov/go-openai"
)

func (w *web) HandleGetFakeNews(c *gin.Context) {
	title := "Fake News | Rasende"
	onlyGrid := StringForm(c, "only-grid", "false") == "true"
	cursorQuery := c.Query("cursor")
	var publishedOffset *time.Time
	var votesOffset int
	if cursorQuery != "" {
		cursorQueryParts := strings.Split(cursorQuery, "¤")
		_publishedOffset, err := time.Parse(time.RFC3339Nano, cursorQueryParts[0])
		if err != nil {
			log.Printf("error parsing cursor: %v", err)
		}
		if err == nil {
			publishedOffset = &_publishedOffset
		}
		if len(cursorQueryParts) >= 2 {
			votesOffset, err = strconv.Atoi(cursorQueryParts[1])
			if err != nil {
				log.Printf("error parsing cursor int: %v", err)
			}
		}
	}
	limit := IntQuery(c, "limit", 5)
	if limit > 5 {
		limit = 5
	}
	sorting := StringQuery(c, "sorting", "popular")
	var fakeNews []core.FakeNewsDto = []core.FakeNewsDto{}
	var err error
	if sorting == "popular" {
		fakeNews, err = w.service.GetPopularFakeNews(limit, publishedOffset, votesOffset)
	} else {
		fakeNews, err = w.service.GetRecentFakeNews(limit, publishedOffset)
	}
	if err != nil {
		log.Printf("error getting highlighted fake news: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if len(fakeNews) == 0 {
		model := components.FakeNewsViewModel{
			Base:     w.getBaseModel(c, title),
			FakeNews: []core.FakeNewsDto{},
			OnlyGrid: onlyGrid,
		}
		c.HTML(http.StatusOK, "", components.FakeNews(model))
		return
	}
	lastFakeNews := fakeNews[len(fakeNews)-1]
	cursor := fmt.Sprintf("%v¤%v", lastFakeNews.Published.Format(time.RFC3339Nano), lastFakeNews.Votes)
	// If returned items is less than limit, return blank cursor so we dont request an empty list on next request
	if len(fakeNews) < limit {
		cursor = ""
	}
	alreadyVoted := getAlreadyVoted(c)
	model := components.FakeNewsViewModel{
		Base:         w.getBaseModel(c, title),
		FakeNews:     fakeNews,
		Cursor:       cursor,
		Sorting:      sorting,
		OnlyGrid:     onlyGrid,
		AlreadyVoted: alreadyVoted,
		Funcs: components.ArticleFuncsModel{
			TimeDifference: getTimeDifference,
			TruncateText:   truncateText,
		},
	}
	c.HTML(http.StatusOK, "", components.FakeNews(model))
}

func (w *web) HandleGetFakeNewsArticle(c *gin.Context) {
	slug, _ := url.QueryUnescape(c.Param("slug"))
	siteId, date, title, err := parseArticleSlug(slug)
	if err != nil {
		log.Printf("error parsing slug '%v': %v", slug, err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	fakeNewsDto, err := w.service.GetFakeNews(siteId, title)
	if err != nil {
		log.Printf("error getting fake news: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if fakeNewsDto == nil {
		err = fmt.Errorf("fake news not found")
		log.Printf("error getting fake news: %v", err)
		c.HTML(http.StatusNotFound, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if fakeNewsDto.Published.Format(time.DateOnly) != date.Format(time.DateOnly) {
		err = fmt.Errorf("invalid date. Got=%v, expected=%v", date, fakeNewsDto.Published)
		log.Printf("returned error because of dates: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	fakeNewsArticleViewModel := components.FakeNewsArticleViewModel{
		Base:     w.getBaseModel(c, fmt.Sprintf("%s | %v | Fake News", fakeNewsDto.Title, fakeNewsDto.SiteName)),
		FakeNews: *fakeNewsDto,
	}
	url := fmt.Sprintf("https://%v%v", c.Request.Host, c.Request.URL.Path)
	fakeNewsArticleViewModel.Base.OpenGraph = &components.BaseOpenGraphModel{
		Title:       fmt.Sprintf("%v | %v", fakeNewsDto.Title, fakeNewsDto.SiteName),
		Image:       *fakeNewsDto.ImageUrl,
		Url:         url,
		Description: truncateText(fakeNewsDto.Content, 100),
	}
	c.HTML(http.StatusOK, "", components.FakeNewsArticle(fakeNewsArticleViewModel))
}

func (w *web) HandleGetTitleGenerator(c *gin.Context) {
	title := "Title Generator | Rasende"
	selectedSiteId := IntQuery(c, "siteId", 0)

	sites, err := w.service.GetSites()
	if err != nil {
		log.Printf("error getting sites: %v", err)
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	var selectedSite core.NewsSite
	if selectedSiteId > 0 {
		_selectedSite, ok := lo.Find(sites, func(s core.NewsSite) bool { return s.Id == selectedSiteId })
		if ok {
			selectedSite = _selectedSite
		}
	}

	c.HTML(http.StatusOK, "", components.TitleGenerator(components.TitleGeneratorViewModel{
		Base:           w.getBaseModel(c, title),
		Sites:          sites,
		SelectedSiteId: selectedSiteId,
		SelectedSite:   selectedSite,
	}))
}

func (w *web) HandleGetSseTitles(c *gin.Context) {
	ctx := c.Request.Context()
	siteId := IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	defaultLimit := 10
	limit := IntQuery(c, "limit", defaultLimit)
	if limit > defaultLimit {
		limit = defaultLimit
	}
	var temperature float32 = 1.0
	cursorQuery := int64(IntQuery(c, "cursor", 0))
	var insertedAtOffset *time.Time
	if cursorQuery > 0 {
		_insertedAtOffset := time.Unix(cursorQuery, 0).UTC()
		insertedAtOffset = &_insertedAtOffset
	}
	log.Println("insertedAtOffset", insertedAtOffset)
	siteInfo, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if siteInfo == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found"), DoNotIncludeLayout: true}))
		return
	}

	items, err := w.service.GetRecentItems(ctx, siteId, limit, insertedAtOffset)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	log.Println("items", items[len(items)-1])
	cursor := fmt.Sprintf("%v", items[len(items)-1].InsertedAt.Unix())
	log.Println("cursor", cursor)
	itemTitles := make([]string, len(items))
	for i, item := range items {
		itemTitles[i] = item.Title
	}
	rand.Shuffle(len(itemTitles), func(i, j int) { itemTitles[i], itemTitles[j] = itemTitles[j], itemTitles[i] })
	stream, err := w.aiClient.GenerateArticleTitles(c.Request.Context(), siteInfo.Name, siteInfo.DescriptionEn, itemTitles, 10, temperature)
	if err != nil {
		log.Printf("openai failed: %v", err)

		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("try again later"), DoNotIncludeLayout: true}))
		} else {
			c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		}
		return
	}

	titles := []string{}
	var currentTitle strings.Builder
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "close")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(io.Writer) bool {
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				for _, title := range titles {
					if len(title) > 0 {
						err = w.service.CreateFakeNews(siteInfo.Id, title)
						if err != nil {
							log.Printf("create fake news failed for site %v, title %v: %v", siteInfo.Name, title, err)
						}
					}
				}
				c.SSEvent("button", RenderToStringCtx(ctx, components.ShowMoreTitlesButton(siteInfo.Id, cursor)))
				c.SSEvent("sse-close", "sse-close")
				c.Writer.Flush()
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				return false
			}
			content := response.Content()
			for _, ch := range content {
				if ch == '\n' {
					title := strings.TrimSpace(currentTitle.String())
					titles = append(titles, title)

					c.SSEvent("title", RenderToStringCtx(ctx, components.GeneratedTitleLink(siteInfo.Id, title)))
					c.Writer.Flush()
					currentTitle.Reset()
				} else {
					currentTitle.WriteRune(ch)
				}
			}
		}
	})
}

func (w *web) HandleGetTitleGeneratorSse(c *gin.Context) {
	siteId := IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	cursor := StringQuery(c, "cursor", "")
	c.HTML(http.StatusOK, "", components.TitlesSse(siteId, cursor, false))
}

func (w *web) HandleGetArticleGenerator(c *gin.Context) {
	log.Println(c.Request.URL.Query())
	pageTitle := "Article Generator | Fake News"
	siteId := IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId")}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId)}))
		return
	}
	articleTitle := StringQuery(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title")}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle)}))
		return
	}

	model := components.ArticleGeneratorViewModel{
		Base:             w.getBaseModel(c, pageTitle),
		Site:             *site,
		Article:          *article,
		ImagePlaceholder: config.PlaceholderImgUrl,
	}
	c.HTML(http.StatusOK, "", components.ArticleGenerator(model))
}

func (w *web) HandleGetSseArticleContent(c *gin.Context) {
	siteId := IntQuery(c, "siteId", 0)
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId), DoNotIncludeLayout: true}))
		return
	}
	articleTitle := StringQuery(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title"), DoNotIncludeLayout: true}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle), DoNotIncludeLayout: true}))
		return
	}

	if len(article.Content) > 0 {
		log.Printf("found existing fake news for site %v title %v", site.Name, article.Title)
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "close")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")
		if article.ImageUrl != nil && *article.ImageUrl != "" {
			c.SSEvent("image", RenderToString(c, components.ArticleImg(*article.ImageUrl, article.Title)))
		} else {
			c.SSEvent("image", RenderToString(c, components.ArticleImg(config.PlaceholderImgUrl, article.Title)))
		}
		c.Stream(func(w io.Writer) bool {
			sseContent := strings.ReplaceAll(article.Content, "\n", "<br />")
			c.SSEvent("content", sseContent)
			c.SSEvent("sse-close", "sse-close")
			c.Writer.Flush()
			return false
		})
		return
	}

	articleImgPromise := pkg.NewPromise(func() (string, error) {
		imgUrl, err := w.aiClient.GenerateImage(c.Request.Context(), site.Name, site.DescriptionEn, article.Title, true)
		if err != nil {
			log.Printf("error maing fake news img: %v", err)
		}
		if imgUrl != "" {
			w.service.SetFakeNewsImgUrl(site.Id, article.Title, imgUrl)
		}
		return imgUrl, err
	})

	var temperature float32 = 1.0
	stream, err := w.aiClient.GenerateArticleContent(c.Request.Context(), site.Name, site.Description, article.Title, temperature)
	if err != nil {
		log.Printf("openai failed: %v", err)
		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			c.HTML(http.StatusTooManyRequests, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		} else {
			c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		}
		return
	}

	var sb strings.Builder
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "close")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Stream(func(io.Writer) bool {
		imgUrlSent := false
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("\nStream finished")
				articleContent := sb.String()
				err = w.service.UpdateFakeNews(site.Id, articleTitle, articleContent)
				if err != nil {
					log.Printf("error saving fake news: %v", err)
				}
				if !imgUrlSent {
					imgUrl, err := articleImgPromise.Get()
					if err != nil {
						log.Printf("error getting openai img: %v", err)
					}
					if imgUrl != "" {
						c.SSEvent("image", RenderToString(c, components.ArticleImg(imgUrl, article.Title)))
						imgUrlSent = true
					}
				}
				c.SSEvent("sse-close", "sse-close")
				c.Writer.Flush()
				return false
			}
			if err != nil {
				log.Printf("\nStream error: %v\n", err)
				c.SSEvent("sse-close", "sse-close")
				c.Writer.Flush()
				return false
			}
			content := response.Content()
			sb.WriteString(content)
			sseContent := fmt.Sprintf("<span>%v</span>", strings.ReplaceAll(content, "\n", "<br />"))
			c.SSEvent("content", sseContent)
			if !imgUrlSent {
				imgUrl, err, articleImgOk := articleImgPromise.Poll()
				if articleImgOk {
					if err != nil {
						log.Printf("error getting openai img: %v", err)
					}
					if imgUrl != "" {
						c.SSEvent("image", RenderToString(c, components.ArticleImg(imgUrl, article.Title)))
						imgUrlSent = true
					}
				}
			}
			c.Writer.Flush()
		}
	})
}

func (w *web) HandlePostPublishFakeNews(c *gin.Context) {
	isAdmin := auth.IsAdmin(c)
	siteId := IntForm(c, "siteId", 0)
	// TODO: instead of returning html with error, do redirect with flash error
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId), DoNotIncludeLayout: true}))
		return
	}
	articleTitle := StringForm(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title"), DoNotIncludeLayout: true}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle), DoNotIncludeLayout: true}))
		return
	}

	// only admin can set a fake news highlighted = false
	var newHighlighted bool
	if article.Highlighted && isAdmin {
		newHighlighted = false
	} else {
		newHighlighted = !article.Highlighted
	}
	err = w.service.SetFakeNewsHighlighted(site.Id, article.Title, newHighlighted)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	article.Highlighted = newHighlighted
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/fake-news/%v", article.Slug()))
}

func (w *web) HandlePostResetContent(c *gin.Context) {
	redirectPath := RefererOrDefault(c, "/")
	if !auth.IsAdmin(c) {
		AddFlashWarn(c, "Requires admin")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	siteId := IntForm(c, "siteId", 0)
	if siteId == 0 {
		AddFlashWarn(c, "missing site")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	title := StringForm(c, "title", "")
	if title == "" {
		AddFlashWarn(c, "missing title")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	err := w.service.ResetFakeNewsContent(siteId, title)
	if err != nil {
		AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}

	c.Redirect(http.StatusSeeOther, redirectPath)
}

func (w *web) HandlePostArticleVote(c *gin.Context) {
	siteId := IntForm(c, "siteId", 0)
	// TODO: instead of returning html with error, do redirect with error query
	if siteId == 0 {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid siteId"), DoNotIncludeLayout: true}))
		return
	}
	site, err := w.service.GetSiteInfoById(siteId)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if site == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("site not found for id %v", siteId), DoNotIncludeLayout: true}))
		return
	}
	articleTitle := StringForm(c, "title", "")
	if articleTitle == "" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("missing title"), DoNotIncludeLayout: true}))
		return
	}

	article, err := w.service.GetFakeNews(site.Id, articleTitle)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: err, DoNotIncludeLayout: true}))
		return
	}
	if article == nil {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("article not found for title %v", articleTitle), DoNotIncludeLayout: true}))
		return
	}

	direction := c.Request.FormValue("direction")
	if direction != "up" && direction != "down" {
		c.HTML(http.StatusBadRequest, "", components.Error(components.ErrorModel{Base: w.getBaseModel(c, ""), Err: fmt.Errorf("invalid vote %v", direction), DoNotIncludeLayout: true}))
	}
	up := direction == "up"
	vote := -1
	if up {
		vote = 1
	}

	updatedVotes, err := w.service.VoteFakeNews(site.Id, article.Title, vote)
	if err != nil {
		c.JSON(500, err.Error())
		return
	}
	article.Votes = updatedVotes
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(fmt.Sprintf("VOTED.%v", article.Id()), direction, 3600*24, "/", "", true, true)
	alreadyVoted := getAlreadyVoted(c)
	alreadyVoted[article.Id()] = direction
	c.HTML(http.StatusOK, "", components.FakeNewsVotes(*article, alreadyVoted))
}

func getAlreadyVoted(c *gin.Context) map[string]string {
	cookies := c.Request.Cookies()
	result := make(map[string]string, 0)
	for _, cookie := range cookies {
		name := cookie.Name
		if strings.HasPrefix(name, "VOTED.") {
			nameParts := strings.Split(name, "VOTED.")
			if len(nameParts) >= 2 {
				id := nameParts[1]
				result[id] = cookie.Value
			}
		}
	}
	return result
}

func parseArticleSlug(slug string) (int, time.Time, string, error) {
	// slug = {site-id:123}-{date:2024-08-19}-{title:article title qwerty}
	var err error
	siteId := 0
	date := time.Time{}
	title := ""
	parts := strings.Split(slug, "-")
	log.Println(len(parts), parts)
	if len(parts) < 4 {
		return siteId, date, title, fmt.Errorf("invalid slug")
	}
	siteId, err = strconv.Atoi(parts[0])
	if err != nil {
		return siteId, date, title, fmt.Errorf("error parsing site id: %w", err)
	}

	year := parts[1]
	month := parts[2]
	day := parts[3]
	date, err = time.Parse("2006-01-02", fmt.Sprintf("%v-%v-%v", year, month, day))
	if err != nil {
		return siteId, date, title, fmt.Errorf("error parsing date: %w", err)
	}

	title = strings.Join(parts[4:], "-")
	return siteId, date, title, nil
}

func getTimeDifference(date time.Time) string {
	now := time.Now()
	diff := now.Sub(date)

	switch {
	case diff < time.Minute:
		return fmt.Sprintf("%ds", int(diff.Seconds()))
	case diff < time.Hour:
		return fmt.Sprintf("%dm", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh", int(diff.Hours()))
	default:
		yearFormat := ""
		if date.Year() != now.Year() {
			yearFormat = " 2006"
		}
		return date.Format("Jan 2" + yearFormat)
	}
}

func truncateText(text string, maxLength int) string {
	lastSpaceIx := maxLength
	len := 0
	for i, r := range text {
		if unicode.IsSpace(r) {
			lastSpaceIx = i
		}
		len++
		if len > maxLength {
			return text[:lastSpaceIx] + "..."
		}
	}
	// If here, string is shorter or equal to maxLen
	return text
}
