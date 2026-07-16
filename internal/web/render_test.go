package web

import (
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
)

// html/template resolves template names and struct fields at execute time, not
// at parse time, so a typo in a branch that only an admin ever sees would
// otherwise reach production. Execute every template against a model that
// exercises both sides of each conditional.
func TestTemplatesExecute(t *testing.T) {
	tmpls, err := (&Renderer{fsys: templateFS, pattern: "templates/*.html"}).parse()
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
		Path:       "/da/fake-news",
		Lang:       "da",
		Editions:   []components.Edition{{Path: "/en/fake-news", Text: "In English"}},
		Title:      "Rasende",
		FlashInfo:  []string{"info"},
		FlashWarn:  []string{"warn"},
		FlashError: []string{"error"},
	}
	adminBase := base
	adminBase.IsAdmin = true
	adminBase.IsAnonymousUser = false
	adminBase.OpenGraph = &components.BaseOpenGraphModel{Title: "t", Image: imgUrl, Url: "u", Description: "d"}

	// The credit only renders on the index, so the layout has to be exercised
	// both with and without it.
	anonBase := base
	anonBase.IsAnonymousUser = true
	anonBase.ShowCredit = true

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

	// Every case runs in every edition. The template sets differ only in their
	// FuncMap, but that is exactly where {{t}} lives, so a key that only one
	// catalog defines would otherwise pass here and fail in the other language.
	for _, l := range lang.All {
		tmpl := tmpls[l.Code]
		if tmpl == nil {
			t.Fatalf("no template set for %q", l.Code)
		}
		for _, tc := range cases {
			if err := tmpl.ExecuteTemplate(io.Discard, tc.name, tc.data); err != nil {
				t.Errorf("executing %q in %q: %v", tc.name, l.Code, err)
			}
		}
	}
}

// tKeyPattern finds the {{t "..."}} calls in the template sources.
var tKeyPattern = regexp.MustCompile(`\{\{-?\s*t\s+"([^"]+)"`)

// Executing the templates only reaches the branches the cases above happen to
// cover, and a missing key renders as the key itself rather than failing. So scan
// the sources instead: every key a template asks for must exist in every
// catalog, and every key a catalog defines must be asked for by someone.
func TestCatalogsCoverTemplates(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatalf("reading templates: %v", err)
	}

	used := map[string]string{} // key -> the file that uses it
	for _, entry := range entries {
		body, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatalf("reading %v: %v", entry.Name(), err)
		}
		for _, match := range tKeyPattern.FindAllStringSubmatch(string(body), -1) {
			used[match[1]] = entry.Name()
		}
	}
	if len(used) == 0 {
		t.Fatal("found no {{t}} keys in the templates; the pattern is probably wrong")
	}

	for _, l := range lang.All {
		defined := make(map[string]bool, len(l.Keys()))
		for _, key := range l.Keys() {
			defined[key] = true
		}
		for key, file := range used {
			if !defined[key] {
				t.Errorf("%v uses key %q, which the %q catalog does not define", file, key, l.Code)
			}
		}
	}

	// The Go side uses the rest — page titles, chart labels, flashes, the
	// sign-in mail — so only report a key no template uses if nothing else
	// plausibly does either. Keeping this loose beats deleting a live key.
	goSidePrefixes := []string{"page.", "chart.", "auth.", "mail.", "error.", "lang.", "brand", "nav."}
	for _, key := range lang.All[0].Keys() {
		if _, ok := used[key]; ok {
			continue
		}
		if slices.ContainsFunc(goSidePrefixes, func(prefix string) bool {
			return strings.HasPrefix(key, prefix)
		}) {
			continue
		}
		t.Errorf("catalog key %q is used by no template and looks like nothing else uses it either", key)
	}
}
