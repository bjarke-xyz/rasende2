// Characterization tests for the HTTP layer.
//
// These pin the behaviour of the router as it is served today, so that the
// migration from gin to net/http can be verified rather than hoped at. They are
// deliberately written against observable output — status codes, headers,
// cookies and, for the streams, the raw bytes on the wire — and not against any
// framework type, so they must survive the port unchanged.
package server_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/mail"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/server"
)

func TestMain(m *testing.M) {
	// The access log and the handlers' own logging would otherwise bury the test
	// output. A failing test reports what it needs itself.
	log.SetOutput(io.Discard)
	m.Run()
}

// --- fixtures ---------------------------------------------------------------

const jobKey = "test-job-key"

var testSite = core.NewsSite{
	Id:          1,
	Name:        "Test Site",
	Language:    "danish",
	Description: "a test site",
	Urls:        []string{"https://example.com/rss"},
}

func testArticle() core.FakeNewsDto {
	return core.FakeNewsDto{
		SiteId:     testSite.Id,
		SiteName:   testSite.Name,
		Title:      "Rasende borger klager",
		Content:    "Første afsnit.\nAndet afsnit.",
		Published:  time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		ImageUrl:   new("https://example.com/img.png"),
		Votes:      3,
		ExternalId: new("abc123"),
	}
}

// fakeService implements only the methods the HTTP layer reaches. Anything else
// panics on the embedded nil interface, which is the point: a handler that grows
// a new dependency should fail loudly here rather than silently go untested.
type fakeService struct {
	core.NewsService

	created []string // titles passed to CreateFakeNews
	votes   int

	// blankContent makes GetFakeNewsByTitle return an article with no content,
	// which is what sends /generate-article down the generating path instead of
	// the cached one.
	blankContent bool
}

func (f *fakeService) GetIndexPageData(ctx context.Context, l lang.Lang) (*core.IndexPageData, error) {
	return &core.IndexPageData{
		SearchResult: &core.SearchResult{Items: []core.RssSearchResult{{
			ItemId: "1", SiteId: 1, SiteName: testSite.Name,
			Title: "Rasende mand", Link: "https://example.com/a", Published: time.Now(),
		}}},
		ChartsResult: &core.ChartsResult{},
	}, nil
}

func (f *fakeService) GetChartData(ctx context.Context, l lang.Lang, query string) (core.ChartsResult, error) {
	return core.ChartsResult{}, nil
}

func (f *fakeService) SearchItems(ctx context.Context, l lang.Lang, query string, searchContent bool, offset, limit int, orderBy string) ([]core.RssSearchResult, error) {
	return []core.RssSearchResult{{
		ItemId: "1", SiteId: 1, SiteName: testSite.Name,
		Title: "Rasende mand " + query, Link: "https://example.com/a", Published: time.Now(),
	}}, nil
}

func (f *fakeService) GetSiteInfos(ctx context.Context, l lang.Lang) ([]core.NewsSite, error) {
	return []core.NewsSite{testSite}, nil
}

func (f *fakeService) GetSiteInfoById(ctx context.Context, id int) (*core.NewsSite, error) {
	if id != testSite.Id {
		return nil, nil
	}
	s := testSite
	return &s, nil
}

func (f *fakeService) GetRecentItems(ctx context.Context, siteId, limit int, offset *time.Time) ([]core.RssItemDto, error) {
	now := time.Now()
	return []core.RssItemDto{{
		ItemId: "1", SiteId: siteId, SiteName: testSite.Name,
		Title: "Et tidligere overskrift", Published: now, InsertedAt: &now,
	}}, nil
}

func (f *fakeService) GetPopularFakeNews(ctx context.Context, limit int, after *time.Time, votes int) ([]core.FakeNewsDto, error) {
	return []core.FakeNewsDto{testArticle()}, nil
}

func (f *fakeService) GetRecentFakeNews(ctx context.Context, limit int, after *time.Time) ([]core.FakeNewsDto, error) {
	return []core.FakeNewsDto{testArticle()}, nil
}

func (f *fakeService) GetFakeNews(ctx context.Context, id string) (*core.FakeNewsDto, error) {
	if id != "abc123" {
		return nil, nil
	}
	a := testArticle()
	return &a, nil
}

func (f *fakeService) GetFakeNewsByTitle(ctx context.Context, siteId int, title string) (*core.FakeNewsDto, error) {
	a := testArticle()
	if title != a.Title {
		return nil, nil
	}
	if f.blankContent {
		a.Content = ""
	}
	return &a, nil
}

func (f *fakeService) CreateFakeNews(ctx context.Context, siteId int, title, externalId string) error {
	f.created = append(f.created, title)
	return nil
}

func (f *fakeService) UpdateFakeNews(ctx context.Context, siteId int, title, content string) error {
	return nil
}
func (f *fakeService) SetFakeNewsImgUrl(ctx context.Context, siteId int, title, imgUrl string) error {
	return nil
}
func (f *fakeService) SetFakeNewsHighlighted(ctx context.Context, siteId int, title string, h bool) error {
	return nil
}
func (f *fakeService) ResetFakeNewsContent(ctx context.Context, siteId int, title string) error {
	return nil
}

func (f *fakeService) VoteFakeNews(ctx context.Context, siteId int, title string, votes int) (int, error) {
	f.votes += votes
	return 3 + f.votes, nil
}

func (f *fakeService) CleanUpFakeNews(ctx context.Context) error         { return nil }
func (f *fakeService) FetchAndSaveNewItems(ctx context.Context) error    { return nil }
func (f *fakeService) RefreshMetrics(ctx context.Context) error          { return nil }
func (f *fakeService) RebuildSearchIndexAndLogError(ctx context.Context) {}
func (f *fakeService) CleanUpFakeNewsAndLogError(ctx context.Context)    {}
func (f *fakeService) Initialise(ctx context.Context)                    {}
func (f *fakeService) Dispose()                                          {}

// fakeAI streams back a fixed script, so the SSE framing is deterministic.
type fakeAI struct {
	core.AiClient

	titleChunks   []string
	contentChunks []string

	// titleStream, when set, is used instead of titleChunks, so a test can hold
	// the stream open and watch what has reached the client so far.
	titleStream core.ChatCompletionStream

	// contentStream and genImage do the same for the article path: they let a
	// test control the order in which the text and the image finish, which is
	// what decides how the two are interleaved.
	contentStream core.ChatCompletionStream
	genImage      func() (string, error)
}

func (f *fakeAI) GenerateArticleTitles(ctx context.Context, site core.NewsSite, prev []string, n int, temp float32) (core.ChatCompletionStream, error) {
	if f.titleStream != nil {
		return f.titleStream, nil
	}
	return core.NewFakeChatCompletionStream(f.titleChunks), nil
}

// gatedStream releases one chunk per send on release, then EOFs when closed. It
// exists to prove that events actually reach the client as they are produced.
type gatedStream struct {
	chunks chan string
}

func (g *gatedStream) Recv() (core.ChatCompletionStreamResponse, error) {
	chunk, ok := <-g.chunks
	if !ok {
		return nil, io.EOF
	}
	return chunkResponse(chunk), nil
}

type chunkResponse string

func (c chunkResponse) Content() string { return string(c) }

func (f *fakeAI) GenerateArticleContent(ctx context.Context, site core.NewsSite, title string, temp float32) (core.ChatCompletionStream, error) {
	if f.contentStream != nil {
		return f.contentStream, nil
	}
	return core.NewFakeChatCompletionStream(f.contentChunks), nil
}

func (f *fakeAI) GenerateImage(ctx context.Context, site core.NewsSite, title string, translate bool) (string, error) {
	if f.genImage != nil {
		return f.genImage()
	}
	return "https://example.com/generated.png", nil
}

// --- harness ----------------------------------------------------------------

type testApp struct {
	handler http.Handler
	svc     *fakeService
	ai      *fakeAI
	cfg     *config.Config
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()

	cfg := &config.Config{
		AppEnv:       config.AppEnvDevelopment,
		DbConnStr:    filepath.Join(t.TempDir(), "test.db"),
		CookieSecret: "test-cookie-secret",
		JobKey:       jobKey,
		SmtpTest:     true,

		// Only used to build the sign-in link and the default post-login redirect.
		// Nothing listens here: the tests either call the handler directly or bind
		// an ephemeral port via httptest.
		BaseUrl: "http://rasende2.test",
	}

	conn, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate("up", conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &fakeService{}
	ai := &fakeAI{
		titleChunks:   []string{"Første overskrift\nAnden overskrift\n"},
		contentChunks: []string{"Noget indhold.\nMere indhold."},
	}

	appCtx := &core.AppContext{
		Config: cfg,
		Infra:  &core.AppInfra{Mail: mail.NewMail(cfg)},
		Deps:   &core.AppDeps{Service: svc, AiClient: ai},
	}

	h, err := server.New(appCtx)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return &testApp{handler: h, svc: svc, ai: ai, cfg: cfg}
}

func (a *testApp) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *testApp) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return a.do(t, httptest.NewRequest(http.MethodGet, path, nil))
}

func (a *testApp) postForm(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return a.do(t, req)
}

// stream drives a request against a real server rather than a ResponseRecorder.
// The streaming handlers need a ResponseWriter that can actually flush, which a
// recorder is not, and reading the response to EOF is the only honest way to see
// the bytes a browser would see.
func (a *testApp) stream(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(a.handler)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return resp, string(body)
}

// --- routes -----------------------------------------------------------------

func TestRoutes(t *testing.T) {
	app := newTestApp(t)
	article := testArticle()

	tests := []struct {
		name     string
		method   string
		path     string
		form     url.Values
		want     int
		wantBody string // substring
	}{
		{name: "health", method: "GET", path: "/health", want: 200, wantBody: `"status":"ok"`},

		{name: "index da", method: "GET", path: "/da", want: 200, wantBody: "<html"},
		{name: "index en", method: "GET", path: "/en", want: 200, wantBody: "<html"},
		{name: "search page", method: "GET", path: "/da/search", want: 200, wantBody: "<html"},
		{name: "fake news list", method: "GET", path: "/da/fake-news", want: 200, wantBody: "<html"},
		{name: "title generator", method: "GET", path: "/da/title-generator", want: 200, wantBody: "<html"},
		// Login is delegated: /login redirects to the OIDC provider's /authorize.
		{name: "login redirect", method: "GET", path: "/da/login", want: 303, wantBody: "/authorize"},

		{
			name: "article page", method: "GET",
			path: "/da/fake-news/" + article.Slug(),
			want: 200, wantBody: article.Title,
		},
		{
			name: "article generator", method: "GET",
			path: "/da/article-generator?siteId=1&title=" + url.QueryEscape(article.Title),
			want: 200, wantBody: "<html",
		},
		{
			name: "titles sse shell", method: "GET",
			path: "/da/generate-titles-sse?siteId=1",
			want: 200, wantBody: "sse",
		},

		{name: "search results", method: "POST", path: "/da/search", form: url.Values{"search": {"rasende"}}, want: 200, wantBody: "Rasende mand rasende"},

		// Bad input.
		{name: "article generator without site", method: "GET", path: "/da/article-generator", want: 400},
		{name: "titles sse without site", method: "GET", path: "/da/generate-titles-sse", want: 400},
		{name: "sse titles without site", method: "GET", path: "/da/generate-titles", want: 400},
		{name: "unknown path", method: "GET", path: "/da/nope", want: 404},
		{name: "unknown root path", method: "GET", path: "/robots.txt", want: 404},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tc.method == "POST" {
				rec = app.postForm(t, tc.path, tc.form)
			} else {
				rec = app.get(t, tc.path)
			}
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d\nbody: %s", rec.Code, tc.want, truncate(rec.Body.String()))
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body does not contain %q\nbody: %s", tc.wantBody, truncate(rec.Body.String()))
			}
		})
	}
}

// HEAD is registered explicitly today; under ServeMux a GET pattern serves it
// for free. Either way it must answer.
func TestHeadIndex(t *testing.T) {
	app := newTestApp(t)
	rec := app.do(t, httptest.NewRequest(http.MethodHead, "/da", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /da = %d, want 200", rec.Code)
	}
}

func TestRedirects(t *testing.T) {
	app := newTestApp(t)

	tests := []struct {
		path     string
		want     int
		location string
	}{
		{path: "/", want: http.StatusFound, location: "/da"},
		{path: "/search", want: http.StatusMovedPermanently, location: "/da/search"},
		{path: "/fake-news", want: http.StatusMovedPermanently, location: "/da/fake-news"},
		{path: "/title-generator", want: http.StatusMovedPermanently, location: "/da/title-generator"},
		{path: "/article-generator", want: http.StatusMovedPermanently, location: "/da/article-generator"},
		{path: "/login", want: http.StatusMovedPermanently, location: "/da/login"},
		{path: "/fake-news/abc123-slug", want: http.StatusMovedPermanently, location: "/da/fake-news/abc123-slug"},

		// Query strings survive the redirect.
		{path: "/search?q=rasende", want: http.StatusMovedPermanently, location: "/da/search?q=rasende"},

		// Trailing slash on an edition root. Gin 301s this; ServeMux must too.
		{path: "/da/", want: http.StatusMovedPermanently, location: "/da"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := app.get(t, tc.path)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
		})
	}
}

func TestStaticAssets(t *testing.T) {
	app := newTestApp(t)

	t.Run("css is immutable", func(t *testing.T) {
		rec := app.get(t, "/static/css/style.css")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("Cache-Control = %q", got)
		}
	})

	t.Run("favicon", func(t *testing.T) {
		if rec := app.get(t, "/favicon.ico"); rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}

// --- SSE --------------------------------------------------------------------

// The SSE payloads are rendered HTML templates, and those are multi-line. The
// encoder must prefix *every* line with "data:" — a continuation line without it
// is dropped by the browser without any error, so this is asserted on raw bytes.
func TestSseTitlesFraming(t *testing.T) {
	app := newTestApp(t)

	resp, body := app.stream(t, "/da/generate-titles?siteId=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	assertWellFormedSSE(t, body)

	for _, want := range []string{"event:title", "event:button", "event:sse-close"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q\n%s", want, body)
		}
	}

	// Both titles from the scripted stream were emitted and persisted.
	if len(app.svc.created) != 2 {
		t.Errorf("CreateFakeNews called %d times, want 2 (%v)", len(app.svc.created), app.svc.created)
	}
	for _, title := range []string{"Første overskrift", "Anden overskrift"} {
		if !strings.Contains(body, title) {
			t.Errorf("stream missing title %q", title)
		}
	}
}

// The middleware wraps http.ResponseWriter to record the status code, and a
// wrapper that does not expose Unwrap silently costs the stream its ability to
// flush: every event still arrives, but only when the handler returns, so the
// page sits blank and then fills in all at once. Reading to EOF cannot see that.
// This reads the first event while the stream is deliberately still open.
func TestSseStreamsIncrementally(t *testing.T) {
	app := newTestApp(t)
	gate := &gatedStream{chunks: make(chan string)}
	app.ai.titleStream = gate

	srv := httptest.NewServer(app.handler)
	defer srv.Close()

	// Produce exactly one title and then leave the stream open. This has to run
	// concurrently with the Get: no response header goes out until the handler
	// writes its first event, so the client is still blocked when this is sent.
	go func() { gate.chunks <- "Første overskrift\n" }()

	resp, err := srv.Client().Get(srv.URL + "/da/generate-titles?siteId=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	firstEvent := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := resp.Body.Read(buf)
		if err != nil {
			return
		}
		firstEvent <- string(buf[:n])
	}()

	select {
	case got := <-firstEvent:
		if !strings.Contains(got, "Første overskrift") {
			t.Errorf("first flushed chunk does not carry the title: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no bytes reached the client while the stream was open: the event was buffered, not flushed")
	}

	close(gate.chunks)
}

// A failed image generation must be logged once, not once per streamed token.
//
// The image and the text are generated concurrently, and the handler polls the
// image on every chunk of text until it has one. The failure case used to leave
// the "still waiting" flag set forever, so once the image had failed, every
// remaining token of the article re-polled the already-resolved promise and
// re-logged the same error — a full article produced hundreds of identical
// lines. Reproducing it needs the image to lose the race, hence the gate.
func TestSseArticleImageFailureLoggedOnce(t *testing.T) {
	app := newTestApp(t)
	app.svc.blankContent = true // take the generating path, not the cached one

	imgFailed := make(chan struct{})
	app.ai.genImage = func() (string, error) {
		defer close(imgFailed)
		return "", fmt.Errorf("image generation returned 0 results")
	}

	gate := &gatedStream{chunks: make(chan string)}
	app.ai.contentStream = gate

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	srv := httptest.NewServer(app.handler)
	defer srv.Close()

	// Hold the text back until the image has already failed, then stream the rest
	// of the article past the resolved promise. Each of these chunks is one more
	// chance to re-log. The count is well above what is needed to see the bug
	// (the unfixed handler logs one line per chunk) because the promise marks
	// itself resolved just after genImage returns, so the first chunk or two can
	// still slip through the window before the bug is even reachable.
	const chunks = 50
	go func() {
		<-imgFailed
		for range chunks {
			gate.chunks <- "afsnit "
		}
		close(gate.chunks)
	}()

	resp, err := srv.Client().Get(srv.URL + "/da/generate-article?siteId=1&title=" + url.QueryEscape(testArticle().Title))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if got := strings.Count(logs.String(), "error getting LLM img"); got != 1 {
		t.Errorf("image failure logged %d times, want 1 (one line per streamed token is the bug)", got)
	}
	// The article still has to stream and close cleanly despite the failed image.
	if !strings.Contains(string(body), "event:sse-close") {
		t.Errorf("stream did not close cleanly:\n%s", body)
	}
	if !strings.Contains(string(body), "afsnit") {
		t.Errorf("article text did not reach the client:\n%s", body)
	}
}

func TestSseArticleContentCached(t *testing.T) {
	app := newTestApp(t)
	article := testArticle() // has Content, so this takes the cached path

	resp, body := app.stream(t, "/da/generate-article?siteId=1&title="+url.QueryEscape(article.Title))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	assertWellFormedSSE(t, body)

	for _, want := range []string{"event:image", "event:content", "event:sse-close"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q\n%s", want, body)
		}
	}
	// Newlines in the article body are turned into <br /> before framing.
	if !strings.Contains(body, "<br />") {
		t.Errorf("expected <br /> in content event\n%s", body)
	}
}

// assertWellFormedSSE checks the wire format: every non-blank line belongs to a
// field, which is what gin-contrib/sse's "\n" -> "\ndata:" replacer guarantees.
func assertWellFormedSSE(t *testing.T, body string) {
	t.Helper()
	if body == "" {
		t.Fatal("empty stream")
	}
	for i, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue // event separator
		}
		if !strings.HasPrefix(line, "data:") &&
			!strings.HasPrefix(line, "event:") &&
			!strings.HasPrefix(line, "id:") &&
			!strings.HasPrefix(line, "retry:") &&
			!strings.HasPrefix(line, ":") {
			t.Fatalf("line %d is not a valid SSE field, so the browser would drop it:\n  %q\nfull stream:\n%s", i+1, line, body)
		}
	}
}

// --- cookies and session ----------------------------------------------------

func TestVoteSetsCookie(t *testing.T) {
	app := newTestApp(t)
	article := testArticle()

	rec := app.postForm(t, "/da/vote-article", url.Values{
		"siteId":    {"1"},
		"title":     {article.Title},
		"direction": {"up"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, truncate(rec.Body.String()))
	}

	wantName := "VOTED." + article.Identifier()
	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == wantName {
			got = c
		}
	}
	if got == nil {
		t.Fatalf("no %q cookie; got %v", wantName, rec.Result().Cookies())
	}
	if got.Value != "up" {
		t.Errorf("value = %q, want %q", got.Value, "up")
	}
	if !got.HttpOnly || !got.Secure {
		t.Errorf("want HttpOnly and Secure, got HttpOnly=%v Secure=%v", got.HttpOnly, got.Secure)
	}
	if got.Path != "/" {
		t.Errorf("path = %q, want /", got.Path)
	}
	if got.MaxAge != 3600*24 {
		t.Errorf("max-age = %d, want %d", got.MaxAge, 3600*24)
	}
}

// The session cookie is the one piece of state that must survive the port
// byte-for-byte: it is signed with COOKIE_SECRET and holds the login. Here we
// only pin that a session round-trips through the middleware, and that the
// cookie is named what it has always been named.
func TestSessionRoundTrip(t *testing.T) {
	app := newTestApp(t)

	// Logout writes an info flash into the session — the simplest way to make the
	// stack set a session cookie.
	rec := app.postForm(t, "/da/logout", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "mysession" {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("no session cookie; got %v", rec.Result().Cookies())
	}

	// Replaying it must render the flash, then consume it.
	req := httptest.NewRequest(http.MethodGet, "/da", nil)
	req.AddCookie(session)
	rec2 := app.do(t, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "flash-info") {
		t.Errorf("expected the logout flash to render\n%s", truncate(rec2.Body.String()))
	}
}

func TestLogoutRedirects(t *testing.T) {
	app := newTestApp(t)
	rec := app.postForm(t, "/da/logout", nil)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rec.Code)
	}
}

// --- api --------------------------------------------------------------------

func TestApiRequiresJobKey(t *testing.T) {
	app := newTestApp(t)

	paths := []string{
		"/api/job",
		"/api/admin/rebuild-index",
		"/api/admin/auto-generate-fake-news",
		"/api/admin/clean-fake-news",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Run("no key", func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, path, nil)
				if rec := app.do(t, req); rec.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", rec.Code)
				}
			})
			t.Run("wrong key", func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, path, nil)
				req.Header.Set("Authorization", "nope")
				if rec := app.do(t, req); rec.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", rec.Code)
				}
			})
		})
	}
}

func TestApiCleanFakeNewsWithKey(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/clean-fake-news", nil)
	req.Header.Set("Authorization", jobKey)
	rec := app.do(t, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200\n%s", rec.Code, truncate(rec.Body.String()))
	}
}

func truncate(s string) string {
	if len(s) > 600 {
		return s[:600] + fmt.Sprintf("... (%d bytes)", len(s))
	}
	return s
}
