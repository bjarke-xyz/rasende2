// Package components holds the view models passed to the templates in
// internal/web/templates. Each page template renders exactly one of these.
package components

import "github.com/bjarke-xyz/rasende2/internal/core"

type BaseOpenGraphModel struct {
	Title       string
	Image       string
	Url         string
	Description string
}

type BaseViewModel struct {
	Path          string
	UnixBuildTime int64
	Title         string
	OpenGraph     *BaseOpenGraphModel
	IncludeLayout bool
	FlashInfo     []string
	FlashWarn     []string
	FlashError    []string

	UserId          int64
	IsAdmin         bool
	IsAnonymousUser bool
}

// IsCurrent reports whether path is the page being rendered, so the header can
// mark the active link.
func (b BaseViewModel) IsCurrent(path string) bool { return b.Path == path }

// Flash is one group of flash messages sharing a severity.
type Flash struct {
	Kind string // core.FlashType*, lowercased into a CSS class by the layout
	Msgs []string
}

// Flashes returns the non-empty flash groups, most severe first.
func (b BaseViewModel) Flashes() []Flash {
	all := []Flash{
		{core.FlashTypeError, b.FlashError},
		{core.FlashTypeWarn, b.FlashWarn},
		{core.FlashTypeInfo, b.FlashInfo},
	}
	flashes := make([]Flash, 0, len(all))
	for _, f := range all {
		if len(f.Msgs) > 0 {
			flashes = append(flashes, f)
		}
	}
	return flashes
}

type ErrorModel struct {
	Base BaseViewModel
	Err  error
}

// Message is the error text. ErrorModel carries an error rather than a string
// because the handlers already have one; templates cannot call Error() on a nil
// interface, so guard here.
func (e ErrorModel) Message() string {
	if e.Err == nil {
		return "unknown error"
	}
	return e.Err.Error()
}

type IndexModel struct {
	Base          BaseViewModel
	SearchResults core.SearchResult
	ChartsResult  core.ChartsResult
}

// Latest is the most recent match, or nil when nothing was found.
func (m IndexModel) Latest() *core.RssSearchResult {
	if len(m.SearchResults.Items) == 0 {
		return nil
	}
	return &m.SearchResults.Items[0]
}

// Earlier is everything but the latest match.
func (m IndexModel) Earlier() []core.RssSearchResult {
	if len(m.SearchResults.Items) == 0 {
		return nil
	}
	return m.SearchResults.Items[1:]
}

type SearchViewModel struct {
	Base BaseViewModel
}

type SearchResultsViewModel struct {
	SearchResults core.SearchResult
	ChartsResult  core.ChartsResult
	NextOffset    int
	Search        string
	IncludeCharts bool
}

type LoginViewModel struct {
	Base       BaseViewModel
	Password   bool
	OTP        bool
	Email      string
	ReturnPath string
}

type FakeNewsViewModel struct {
	Base         BaseViewModel
	FakeNews     []core.FakeNewsDto
	Cursor       string
	Sorting      string
	AlreadyVoted map[string]string // article identifier -> "up" | "down"
}

// Items pairs each article with the viewer's vote, which the card and vote
// widget both need.
func (m FakeNewsViewModel) Items() []FakeNewsItemModel {
	items := make([]FakeNewsItemModel, len(m.FakeNews))
	for i, fn := range m.FakeNews {
		items[i] = FakeNewsItemModel{FakeNews: fn, AlreadyVoted: m.AlreadyVoted}
	}
	return items
}

// FakeNewsItemModel backs both the article card and the standalone vote widget
// that htmx swaps in after a vote.
type FakeNewsItemModel struct {
	FakeNews     core.FakeNewsDto
	AlreadyVoted map[string]string
}

// Identifier re-exposes core.FakeNewsDto.Identifier, which has a pointer
// receiver and so cannot be called on a struct field from a template.
func (m FakeNewsItemModel) Identifier() string { return m.FakeNews.Identifier() }

func (m FakeNewsItemModel) HasVotedUp() bool   { return m.AlreadyVoted[m.Identifier()] == "up" }
func (m FakeNewsItemModel) HasVotedDown() bool { return m.AlreadyVoted[m.Identifier()] == "down" }

type FakeNewsArticleViewModel struct {
	Base     BaseViewModel
	FakeNews core.FakeNewsDto
}

type TitleGeneratorViewModel struct {
	Base           BaseViewModel
	Sites          []core.NewsSite
	SelectedSiteId int
	SelectedSite   core.NewsSite
}

type TitlesSseModel struct {
	SiteId      int
	Cursor      string
	Placeholder bool
}

type ShowMoreTitlesModel struct {
	SiteId int
	Cursor string
}

type GeneratedTitleModel struct {
	SiteId int
	Title  string
}

type ArticleGeneratorViewModel struct {
	Base             BaseViewModel
	Site             core.NewsSite
	Article          core.FakeNewsDto
	ImagePlaceholder string
}

type ArticleImgModel struct {
	Src string
	Alt string
}
