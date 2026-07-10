package web

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
)

// html/template resolves template names and struct fields at execute time, not
// at parse time, so a typo in a branch that only an admin ever sees would
// otherwise reach production. Execute every template against a model that
// exercises both sides of each conditional.
func TestTemplatesExecute(t *testing.T) {
	tmpl, err := (&Renderer{fsys: templateFS, pattern: "templates/*.html"}).parse()
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}

	published := time.Date(2024, 8, 19, 12, 0, 0, 0, time.UTC)
	imgUrl := "https://example.com/img.png"
	externalId := "abc123"
	article := core.FakeNewsDto{
		SiteName:   "DR",
		Title:      "En overskrift",
		Content:    "Første afsnit.\nAndet afsnit.",
		Published:  published,
		SiteId:     1,
		ImageUrl:   &imgUrl,
		Votes:      3,
		ExternalId: &externalId,
	}
	item := core.RssSearchResult{SiteName: "DR", Title: "Rasende borger", Link: "https://example.com/a", Published: published}
	charts := core.ChartsResult{Charts: []core.ChartResult{{
		Type:     "doughnut",
		Title:    "Raseri",
		Labels:   []string{"DR"},
		Datasets: []core.ChartDataset{{Label: "Raseriudbrud", Data: []int{1}}},
	}}}
	base := components.BaseViewModel{
		Path:       "/fake-news",
		Title:      "Rasende",
		FlashInfo:  []string{"info"},
		FlashWarn:  []string{"warn"},
		FlashError: []string{"error"},
	}
	adminBase := base
	adminBase.IsAdmin = true
	adminBase.IsAnonymousUser = false
	adminBase.OpenGraph = &components.BaseOpenGraphModel{Title: "t", Image: imgUrl, Url: "u", Description: "d"}

	anonBase := base
	anonBase.IsAnonymousUser = true

	// Both vote states, so the "voted" branches render.
	voted := components.FakeNewsItemModel{FakeNews: article, AlreadyVoted: map[string]string{}}
	votedUp := components.FakeNewsItemModel{FakeNews: article, AlreadyVoted: map[string]string{article.Identifier(): "up"}}

	cases := []struct {
		name string
		data any
	}{
		{"layout", layoutData{BaseViewModel: adminBase, Content: "<p>hi</p>"}},
		{"layout", layoutData{BaseViewModel: anonBase, Content: "<p>hi</p>"}},
		{"error", components.ErrorModel{Err: errors.New("boom")}},
		{"error", components.ErrorModel{}}, // nil error must not panic
		{"login", components.LoginViewModel{Base: base, Password: true, OTP: true}},
		{"login", components.LoginViewModel{Base: base}},
		{"index", components.IndexModel{Base: base, SearchResults: core.SearchResult{Items: []core.RssSearchResult{item, item}}, ChartsResult: charts}},
		{"index", components.IndexModel{Base: base}}, // no results: "Ingen raseri!"
		{"search", components.SearchViewModel{Base: base}},
		{"searchResults", components.SearchResultsViewModel{SearchResults: core.SearchResult{Items: []core.RssSearchResult{item}}, ChartsResult: charts, NextOffset: 100, Search: "rasende", IncludeCharts: true}},
		{"searchResults", components.SearchResultsViewModel{IncludeCharts: false}},
		{"fakeNews", components.FakeNewsViewModel{Base: base, FakeNews: []core.FakeNewsDto{article}, Cursor: "c", Sorting: "popular"}},
		{"fakeNewsGrid", components.FakeNewsViewModel{FakeNews: []core.FakeNewsDto{article}, Sorting: "latest"}}, // empty cursor: no button
		{"fakeNewsArticle", components.FakeNewsArticleViewModel{Base: adminBase, FakeNews: article}},
		{"fakeNewsArticle", components.FakeNewsArticleViewModel{Base: base, FakeNews: core.FakeNewsDto{ExternalId: &externalId}}}, // nil ImageUrl
		{"fakeNewsVotes", voted},
		{"fakeNewsVotes", votedUp},
		{"articleCard", voted},
		{"articleImg", components.ArticleImgModel{Src: imgUrl, Alt: "alt"}},
		{"titleGenerator", components.TitleGeneratorViewModel{Base: base, Sites: []core.NewsSite{{Id: 1, Name: "DR"}}, SelectedSiteId: 1, SelectedSite: core.NewsSite{Id: 1, Name: "DR"}}},
		{"titleGenerator", components.TitleGeneratorViewModel{Base: base, Sites: []core.NewsSite{{Id: 1, Name: "DR"}}}}, // nothing selected
		{"titlesSse", components.TitlesSseModel{SiteId: 1, Cursor: "0", Placeholder: true}},
		{"titlesSse", components.TitlesSseModel{SiteId: 1}},
		{"showMoreTitlesButton", components.ShowMoreTitlesModel{SiteId: 1, Cursor: "0"}},
		{"generatedTitleLink", components.GeneratedTitleModel{SiteId: 1, Title: "En titel"}},
		{"articleGenerator", components.ArticleGeneratorViewModel{Base: base, Site: core.NewsSite{Id: 1, Name: "DR"}, Article: article, ImagePlaceholder: imgUrl}},
		{"articleGenerator", components.ArticleGeneratorViewModel{Base: base, Article: core.FakeNewsDto{Highlighted: true}}}, // no publish button
		{"charts", charts},
		{"badge", "DR"},
		{"itemLink", item},
		{"barsSvg", nil},
	}

	for _, tc := range cases {
		if err := tmpl.ExecuteTemplate(io.Discard, tc.name, tc.data); err != nil {
			t.Errorf("executing %q: %v", tc.name, err)
		}
	}
}
