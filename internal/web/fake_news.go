package web

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/session"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/sashabaranov/go-openai"
)

func (h *web) HandleGetFakeNews(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	title := LangOf(r).T("page.fakeNews")
	onlyGrid := httpx.StringForm(r, "only-grid", "false") == "true"
	cursorQuery := r.URL.Query().Get("cursor")
	var publishedOffset *time.Time
	var votesOffset int
	if cursorQuery != "" {
		cursorQueryParts := strings.Split(cursorQuery, "¤")
		_publishedOffset, err := time.Parse(time.RFC3339Nano, cursorQueryParts[0])
		if err != nil {
			slog.Warn("parsing cursor failed", "error", err)
		}
		if err == nil {
			publishedOffset = &_publishedOffset
		}
		if len(cursorQueryParts) >= 2 {
			votesOffset, err = strconv.Atoi(cursorQueryParts[1])
			if err != nil {
				slog.Warn("parsing cursor int failed", "error", err)
			}
		}
	}
	limit := min(httpx.IntQuery(r, "limit", 5), 5)
	sorting := httpx.StringQuery(r, "sorting", "popular")
	var fakeNews []core.FakeNewsDto = []core.FakeNewsDto{}
	var err error
	if sorting == "popular" {
		fakeNews, err = h.appContext.Deps.Service.GetPopularFakeNews(ctx, limit, publishedOffset, votesOffset)
	} else {
		fakeNews, err = h.appContext.Deps.Service.GetRecentFakeNews(ctx, limit, publishedOffset)
	}
	if err != nil {
		slog.Error("getting highlighted fake news failed", "error", err)
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	cursor := ""
	// If returned items is less than limit, return blank cursor so we dont request an empty list on next request
	if len(fakeNews) > 0 && len(fakeNews) == limit {
		lastFakeNews := fakeNews[len(fakeNews)-1]
		cursor = fmt.Sprintf("%v¤%v", lastFakeNews.Published.Format(time.RFC3339Nano), lastFakeNews.Votes)
	}
	model := components.FakeNewsViewModel{
		Base:         h.getBaseModel(w, r, title),
		FakeNews:     fakeNews,
		Cursor:       cursor,
		Sorting:      sorting,
		AlreadyVoted: getAlreadyVoted(r),
	}
	// "Vis mere" asks for the grid alone, to append to the one already on screen.
	if onlyGrid {
		h.renderer.Partial(w, r, http.StatusOK, "fakeNewsGrid", model)
		return
	}
	h.renderer.Page(w, r, http.StatusOK, "fakeNews", model.Base, model)
}

func (h *web) HandleGetFakeNewsArticle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	querySlug, _ := url.QueryUnescape(r.PathValue("slug"))
	externalId, _, err := parseArticleSlugV2(querySlug)
	if err != nil {
		slog.Warn("parsing slug failed", "slug", querySlug, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	fakeNewsDto, err := h.appContext.Deps.Service.GetFakeNews(ctx, externalId)
	if err != nil {
		slog.Error("getting fake news failed", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if fakeNewsDto == nil {
		err = fmt.Errorf("fake news not found")
		slog.Error("getting fake news failed", "error", err)
		h.renderError(w, r, http.StatusNotFound, err)
		return
	}
	if fakeNewsDto.Slug() != querySlug {
		http.Redirect(w, r, fmt.Sprintf("/%v/fake-news/%v", LangOf(r).Code, fakeNewsDto.Slug()), http.StatusFound)
		return
	}
	// if fakeNewsDto.Published.Format(time.DateOnly) != date.Format(time.DateOnly) {
	// 	err = fmt.Errorf("invalid date. Got=%v, expected=%v", date, fakeNewsDto.Published)
	// 	log.Printf("returned error because of dates: %v", err)
	// 	w.renderError(c, http.StatusInternalServerError, err)
	// 	return
	// }
	fakeNewsArticleViewModel := components.FakeNewsArticleViewModel{
		Base:     h.getBaseModel(w, r, fmt.Sprintf("%s | %v | Fake News", fakeNewsDto.Title, fakeNewsDto.SiteName)),
		FakeNews: *fakeNewsDto,
	}
	url := fmt.Sprintf("https://%v%v", r.Host, r.URL.Path)
	fakeNewsArticleViewModel.Base.OpenGraph = &components.BaseOpenGraphModel{
		Title:       fmt.Sprintf("%v | %v", fakeNewsDto.Title, fakeNewsDto.SiteName),
		Image:       *fakeNewsDto.ImageUrl,
		Url:         url,
		Description: truncateText(fakeNewsDto.Content, 100),
	}
	h.renderer.Page(w, r, http.StatusOK, "fakeNewsArticle", fakeNewsArticleViewModel.Base, fakeNewsArticleViewModel)
}

func (h *web) HandleGetTitleGenerator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	title := LangOf(r).T("page.titleGenerator")
	selectedSiteId := httpx.IntQuery(r, "siteId", 0)

	sites, err := h.appContext.Deps.Service.GetSiteInfos(ctx, LangOf(r))
	if err != nil {
		slog.Error("getting sites failed", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	var selectedSite core.NewsSite
	if selectedSiteId > 0 {
		if i := slices.IndexFunc(sites, func(s core.NewsSite) bool { return s.Id == selectedSiteId }); i >= 0 {
			selectedSite = sites[i]
		}
	}

	model := components.TitleGeneratorViewModel{
		Base:           h.getBaseModel(w, r, title),
		Sites:          sites,
		SelectedSiteId: selectedSiteId,
		SelectedSite:   selectedSite,
	}
	h.renderer.Page(w, r, http.StatusOK, "titleGenerator", model.Base, model)
}

func (h *web) HandleGetSseTitles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteId := httpx.IntQuery(r, "siteId", 0)
	if siteId == 0 {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("invalid siteId"))
		return
	}
	defaultLimit := 10
	limit := min(httpx.IntQuery(r, "limit", defaultLimit), defaultLimit)
	var temperature float32 = 1.0
	cursorQuery := int64(httpx.IntQuery(r, "cursor", 0))
	var insertedAtOffset *time.Time
	if cursorQuery > 0 {
		_insertedAtOffset := time.Unix(cursorQuery, 0).UTC()
		insertedAtOffset = &_insertedAtOffset
	}
	siteInfo, err := h.appContext.Deps.Service.GetSiteInfoById(ctx, siteId)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if siteInfo == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("site not found"))
		return
	}

	items, err := h.appContext.Deps.Service.GetRecentItems(ctx, siteId, limit, insertedAtOffset)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	// A site that is configured but not yet fetched has no items — every new site
	// is in that state until the RSS job first runs. Generate from its description
	// alone rather than refusing: the prompt carries the description too, and the
	// previous titles only sharpen the imitation. The cursor stays empty because
	// there is nothing to page back through.
	cursor := ""
	if len(items) > 0 {
		cursor = fmt.Sprintf("%v", items[len(items)-1].InsertedAt.Unix())
	}
	itemTitles := make([]string, len(items))
	for i, item := range items {
		itemTitles[i] = item.Title
	}
	rand.Shuffle(len(itemTitles), func(i, j int) { itemTitles[i], itemTitles[j] = itemTitles[j], itemTitles[i] })
	stream, err := h.appContext.Deps.AiClient.GenerateArticleTitles(ctx, *siteInfo, itemTitles, 10, temperature)
	if err != nil {
		slog.Error("llm failed", "error", err)

		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			h.renderErrorFragment(w, r, http.StatusInternalServerError, fmt.Errorf("%v", LangOf(r).T("error.tryAgainLater")))
		} else {
			h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		}
		return
	}

	titles := []string{}
	var currentTitle strings.Builder
	httpx.SSEHeaders(w)
	// emitTitle finishes the title being accumulated and streams it to the page.
	// A model puts blank lines between titles, and a blank line is not a title:
	// dropping it here is what keeps the page from filling with empty links.
	emitTitle := func() {
		title := pkg.CleanGeneratedTitle(currentTitle.String())
		currentTitle.Reset()
		if title == "" {
			return
		}
		titles = append(titles, title)
		httpx.SSEvent(w, "title", h.renderer.String(r, "generatedTitleLink", components.GeneratedTitleModel{SiteId: siteInfo.Id, Title: title}))
		httpx.Flush(w)
	}

	for {
		// The visitor closed the tab. Nothing left to stream to.
		if ctx.Err() != nil {
			return
		}
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// The model does not always end its last line with a newline, so the
			// final title is still sitting in the buffer. Without this it is
			// silently dropped.
			emitTitle()
			for _, title := range titles {
				if err := h.appContext.Deps.Service.CreateFakeNews(ctx, siteInfo.Id, title, pkg.NewID()); err != nil {
					slog.Error("create fake news failed", "site", siteInfo.Name, "title", title, "error", err)
				}
			}
			httpx.SSEvent(w, "button", h.renderer.String(r, "showMoreTitlesButton", components.ShowMoreTitlesModel{SiteId: siteInfo.Id, Cursor: cursor}))
			httpx.SSEvent(w, "sse-close", "sse-close")
			httpx.Flush(w)
			return
		}
		if err != nil {
			slog.Error("stream error", "error", err)
			return
		}
		for _, ch := range response.Content() {
			if ch == '\n' {
				emitTitle()
			} else {
				currentTitle.WriteRune(ch)
			}
		}
	}
}

func (h *web) HandleGetTitleGeneratorSse(w http.ResponseWriter, r *http.Request) {
	siteId := httpx.IntQuery(r, "siteId", 0)
	if siteId == 0 {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("invalid siteId"))
		return
	}
	cursor := httpx.StringQuery(r, "cursor", "")
	h.renderer.Partial(w, r, http.StatusOK, "titlesSse", components.TitlesSseModel{SiteId: siteId, Cursor: cursor})
}

func (h *web) HandleGetArticleGenerator(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pageTitle := LangOf(r).T("page.articleGenerator")
	siteId := httpx.IntQuery(r, "siteId", 0)
	if siteId == 0 {
		h.renderError(w, r, http.StatusBadRequest, fmt.Errorf("invalid siteId"))
		return
	}
	site, err := h.appContext.Deps.Service.GetSiteInfoById(ctx, siteId)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if site == nil {
		h.renderError(w, r, http.StatusBadRequest, fmt.Errorf("site not found for id %v", siteId))
		return
	}
	articleTitle := httpx.StringQuery(r, "title", "")
	if articleTitle == "" {
		h.renderError(w, r, http.StatusBadRequest, fmt.Errorf("missing title"))
		return
	}

	article, err := h.appContext.Deps.Service.GetFakeNewsByTitle(ctx, site.Id, articleTitle)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err)
		return
	}
	if article == nil {
		h.renderError(w, r, http.StatusBadRequest, fmt.Errorf("article not found for title %v", articleTitle))
		return
	}

	model := components.ArticleGeneratorViewModel{
		Base:             h.getBaseModel(w, r, pageTitle),
		Site:             *site,
		Article:          *article,
		ImagePlaceholder: config.PlaceholderImgUrl,
	}
	h.renderer.Page(w, r, http.StatusOK, "articleGenerator", model.Base, model)
}

func (h *web) HandleGetSseArticleContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteId := httpx.IntQuery(r, "siteId", 0)
	if siteId == 0 {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("invalid siteId"))
		return
	}
	site, err := h.appContext.Deps.Service.GetSiteInfoById(ctx, siteId)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if site == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("site not found for id %v", siteId))
		return
	}
	articleTitle := httpx.StringQuery(r, "title", "")
	if articleTitle == "" {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("missing title"))
		return
	}

	article, err := h.appContext.Deps.Service.GetFakeNewsByTitle(ctx, site.Id, articleTitle)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if article == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("article not found for title %v", articleTitle))
		return
	}

	if len(article.Content) > 0 {
		slog.Debug("found existing fake news", "site", site.Name, "title", article.Title)
		httpx.SSEHeaders(w)
		imgSrc := config.PlaceholderImgUrl
		if article.ImageUrl != nil && *article.ImageUrl != "" {
			imgSrc = *article.ImageUrl
		}
		httpx.SSEvent(w, "image", h.renderer.String(r, "articleImg", components.ArticleImgModel{Src: imgSrc, Alt: article.Title}))
		httpx.SSEvent(w, "content", strings.ReplaceAll(article.Content, "\n", "<br />"))
		httpx.SSEvent(w, "sse-close", "sse-close")
		httpx.Flush(w)
		return
	}

	// The error is returned, not logged: resolveImage below is what deals with it.
	articleImgPromise := pkg.NewPromise(func() (string, error) {
		imgUrl, err := h.appContext.Deps.AiClient.GenerateImage(ctx, *site, article.Title, true)
		if imgUrl != "" {
			h.appContext.Deps.Service.SetFakeNewsImgUrl(ctx, site.Id, article.Title, imgUrl)
		}
		return imgUrl, err
	})

	var temperature float32 = 1.0
	stream, err := h.appContext.Deps.AiClient.GenerateArticleContent(ctx, *site, article.Title, temperature)
	if err != nil {
		slog.Error("llm failed", "error", err)
		var apiError *openai.APIError
		if errors.As(err, &apiError) && apiError.HTTPStatusCode == 429 {
			h.renderErrorFragment(w, r, http.StatusTooManyRequests, err)
		} else {
			h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		}
		return
	}

	var sb strings.Builder
	httpx.SSEHeaders(w)

	// The image and the text are produced in parallel, so the image is emitted the
	// moment it is ready rather than at any particular point in the text.
	//
	// imgDone means the promise has resolved and its outcome is dealt with —
	// which includes it having failed. It must not mean "an image was sent", or a
	// failed generation leaves the poll below firing on every content token.
	imgDone := false
	resolveImage := func(imgUrl string, err error) {
		imgDone = true
		if err != nil {
			slog.Error("error getting LLM img", "error", err)
		}
		if imgUrl == "" {
			return
		}
		httpx.SSEvent(w, "image", h.renderer.String(r, "articleImg", components.ArticleImgModel{Src: imgUrl, Alt: article.Title}))
	}

	for {
		if ctx.Err() != nil {
			return
		}
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			slog.Debug("stream finished")
			articleContent := sb.String()
			err = h.appContext.Deps.Service.UpdateFakeNews(ctx, site.Id, articleTitle, articleContent)
			if err != nil {
				slog.Error("saving fake news failed", "error", err)
			}
			if !imgDone {
				resolveImage(articleImgPromise.Get())
			}
			httpx.SSEvent(w, "sse-close", "sse-close")
			httpx.Flush(w)
			return
		}
		if err != nil {
			slog.Error("stream error", "error", err)
			httpx.SSEvent(w, "sse-close", "sse-close")
			httpx.Flush(w)
			return
		}
		content := response.Content()
		sb.WriteString(content)
		sseContent := fmt.Sprintf("<span>%v</span>", strings.ReplaceAll(content, "\n", "<br />"))
		httpx.SSEvent(w, "content", sseContent)
		if !imgDone {
			if imgUrl, err, ok := articleImgPromise.Poll(); ok {
				resolveImage(imgUrl, err)
			}
		}
		httpx.Flush(w)
	}
}

func (h *web) HandlePostPublishFakeNews(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	isAdmin := session.IsAdmin(r)
	siteId := httpx.IntForm(r, "siteId", 0)
	// TODO: instead of returning html with error, do redirect with flash error
	if siteId == 0 {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("invalid siteId"))
		return
	}
	site, err := h.appContext.Deps.Service.GetSiteInfoById(ctx, siteId)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if site == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("site not found for id %v", siteId))
		return
	}
	articleTitle := httpx.StringForm(r, "title", "")
	if articleTitle == "" {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("missing title"))
		return
	}

	article, err := h.appContext.Deps.Service.GetFakeNewsByTitle(ctx, site.Id, articleTitle)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if article == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("article not found for title %v", articleTitle))
		return
	}

	// only admin can set a fake news highlighted = false
	var newHighlighted bool
	if article.Highlighted && isAdmin {
		newHighlighted = false
	} else {
		newHighlighted = !article.Highlighted
	}
	err = h.appContext.Deps.Service.SetFakeNewsHighlighted(ctx, site.Id, article.Title, newHighlighted)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	article.Highlighted = newHighlighted
	http.Redirect(w, r, fmt.Sprintf("/%v/fake-news/%v", LangOf(r).Code, article.Slug()), http.StatusSeeOther)
}

func (h *web) HandlePostResetContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	redirectPath := httpx.RefererOrDefault(r, "/")
	if !session.IsAdmin(r) {
		session.AddFlashWarn(w, r, LangOf(r).T("error.requiresAdmin"))
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	siteId := httpx.IntForm(r, "siteId", 0)
	if siteId == 0 {
		session.AddFlashWarn(w, r, "missing site")
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	title := httpx.StringForm(r, "title", "")
	if title == "" {
		session.AddFlashWarn(w, r, "missing title")
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	err := h.appContext.Deps.Service.ResetFakeNewsContent(ctx, siteId, title)
	if err != nil {
		session.AddFlashError(w, r, err)
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, redirectPath, http.StatusSeeOther)
}

func (h *web) HandlePostArticleVote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	siteId := httpx.IntForm(r, "siteId", 0)
	// TODO: instead of returning html with error, do redirect with error query
	if siteId == 0 {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("invalid siteId"))
		return
	}
	site, err := h.appContext.Deps.Service.GetSiteInfoById(ctx, siteId)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if site == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("site not found for id %v", siteId))
		return
	}
	articleTitle := httpx.StringForm(r, "title", "")
	if articleTitle == "" {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("missing title"))
		return
	}

	article, err := h.appContext.Deps.Service.GetFakeNewsByTitle(ctx, site.Id, articleTitle)
	if err != nil {
		h.renderErrorFragment(w, r, http.StatusInternalServerError, err)
		return
	}
	if article == nil {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("article not found for title %v", articleTitle))
		return
	}

	direction := r.FormValue("direction")
	if direction != "up" && direction != "down" {
		h.renderErrorFragment(w, r, http.StatusBadRequest, fmt.Errorf("invalid vote %v", direction))
	}
	up := direction == "up"
	vote := -1
	if up {
		vote = 1
	}

	updatedVotes, err := h.appContext.Deps.Service.VoteFakeNews(ctx, site.Id, article.Title, vote)
	if err != nil {
		httpx.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	article.Votes = updatedVotes
	// The vote is applied server-side regardless; this cookie only drives which
	// arrow the page shows as already pressed.
	http.SetCookie(w, &http.Cookie{
		Name:     fmt.Sprintf("VOTED.%v", article.Identifier()),
		Value:    direction,
		Path:     "/",
		MaxAge:   3600 * 24,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	alreadyVoted := getAlreadyVoted(r)
	alreadyVoted[article.Identifier()] = direction
	h.renderer.Partial(w, r, http.StatusOK, "fakeNewsVotes", components.FakeNewsItemModel{FakeNews: *article, AlreadyVoted: alreadyVoted})
}

func getAlreadyVoted(r *http.Request) map[string]string {
	result := make(map[string]string, 0)
	for _, cookie := range r.Cookies() {
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

func parseArticleSlugV2(slug string) (string, string, error) {
	// slug = {id}-{title}
	externalId := ""
	title := ""
	parts := strings.Split(slug, "-")
	if len(parts) < 2 {
		return externalId, title, fmt.Errorf("invalid slug")
	}
	externalId = parts[0]
	title = strings.Join(parts[1:], "-")
	return externalId, title, nil
}

// func parseArticleSlug(slug string) (int, time.Time, string, error) {
// 	// slug = {site-id:123}-{date:2024-08-19}-{title:article title qwerty}
// 	var err error
// 	siteId := 0
// 	date := time.Time{}
// 	title := ""
// 	parts := strings.Split(slug, "-")
// 	log.Println(len(parts), parts)
// 	if len(parts) < 4 {
// 		return siteId, date, title, fmt.Errorf("invalid slug")
// 	}
// 	siteId, err = strconv.Atoi(parts[0])
// 	if err != nil {
// 		return siteId, date, title, fmt.Errorf("error parsing site id: %w", err)
// 	}

// 	year := parts[1]
// 	month := parts[2]
// 	day := parts[3]
// 	date, err = time.Parse("2006-01-02", fmt.Sprintf("%v-%v-%v", year, month, day))
// 	if err != nil {
// 		return siteId, date, title, fmt.Errorf("error parsing date: %w", err)
// 	}

// 	title = strings.Join(parts[4:], "-")
// 	return siteId, date, title, nil
// }

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
