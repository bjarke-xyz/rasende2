package news

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/repository"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
)

var testSite = core.NewsSite{Id: 1, Name: "Testmedie"}

// newTestSearch builds a migrated, populated database in a temp dir and returns a
// RssSearch over it. Items are inserted through the repository, so this also
// covers the transactional indexing done by InsertItems.
func newTestSearch(t *testing.T, items []core.RssItemDto) *RssSearch {
	t.Helper()
	cfg := &config.Config{DbConnStr: filepath.Join(t.TempDir(), "test.db")}
	conn, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate("up", conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appContext := &core.AppContext{Config: cfg}
	repo := repository.NewSqliteNews(appContext)
	if _, err := repo.InsertItems(context.Background(), testSite, items); err != nil {
		t.Fatalf("insert items: %v", err)
	}
	return NewRssSearch(appContext)
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func item(t *testing.T, id, title, content, published string) core.RssItemDto {
	t.Helper()
	insertedAt := mustTime(t, published)
	return core.RssItemDto{
		ItemId:     id,
		SiteName:   testSite.Name,
		Title:      title,
		Content:    content,
		Link:       "https://example.dk/" + id,
		Published:  insertedAt,
		InsertedAt: &insertedAt,
		SiteId:     testSite.Id,
	}
}

func itemIds(results []core.RssSearchResult) []string {
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.ItemId
	}
	return ids
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func corpus(t *testing.T) []core.RssItemDto {
	return []core.RssItemDto{
		item(t, "a", "Rasende politiker råber ad ministeren", "Han var meget vred i går.", "2024-03-01T10:00:00Z"),
		item(t, "b", "Minister raser over nye tal", "Tallene er for dårlige.", "2024-03-02T10:00:00Z"),
		item(t, "c", "Glad hund finder ny ejer", "Intet raseri her overhovedet.", "2024-03-03T10:00:00Z"),
		item(t, "d", "Rødgrød med fløde til alle", "En rasende god dessert.", "2024-01-05T10:00:00Z"),
	}
}

// The requirement that motivated the whole Go-side analyzer: an inflection of the
// search term must find every other inflection of it.
func TestSearchMatchesDanishInflections(t *testing.T) {
	rssSearch := newTestSearch(t, corpus(t))
	ctx := context.Background()

	for _, query := range []string{"raser", "rasende", "rase"} {
		results, err := rssSearch.Search(ctx, query, false, nil, nil, "published", 10, 0)
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		if got, want := itemIds(results), []string{"a", "b"}; !equal(got, want) {
			t.Errorf("search(%q) title-only = %v, want %v", query, got, want)
		}
	}
}

func TestSearchTitleOnlyExcludesContentMatches(t *testing.T) {
	rssSearch := newTestSearch(t, corpus(t))
	ctx := context.Background()

	// "d" matches only in content, "c" not at all.
	titleOnly, err := rssSearch.Search(ctx, "rasende", false, nil, nil, "published", 10, 0)
	if err != nil {
		t.Fatalf("title-only search: %v", err)
	}
	if got, want := itemIds(titleOnly), []string{"a", "b"}; !equal(got, want) {
		t.Errorf("title-only = %v, want %v", got, want)
	}

	withContent, err := rssSearch.Search(ctx, "rasende", true, nil, nil, "published", 10, 0)
	if err != nil {
		t.Fatalf("content search: %v", err)
	}
	if got, want := itemIds(withContent), []string{"d", "a", "b"}; !equal(got, want) {
		t.Errorf("title+content = %v, want %v", got, want)
	}
}

// A query of nothing but stop words analyzes to zero tokens. That must return no
// results rather than reaching FTS5 as an empty MATCH expression, which is a
// syntax error.
func TestSearchStopWordsOnlyReturnsEmpty(t *testing.T) {
	rssSearch := newTestSearch(t, corpus(t))
	ctx := context.Background()

	results, err := rssSearch.Search(ctx, "og i er det", false, nil, nil, "published", 10, 0)
	if err != nil {
		t.Fatalf("stop word search returned error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("stop word search = %v, want no results", itemIds(results))
	}

	counts, err := rssSearch.CountByDay(ctx, "og i er det", false, nil, nil)
	if err != nil {
		t.Fatalf("stop word CountByDay returned error: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("stop word CountByDay = %v, want none", counts)
	}
}

func TestSearchDateRangeAndOrdering(t *testing.T) {
	rssSearch := newTestSearch(t, corpus(t))
	ctx := context.Background()

	start := mustTime(t, "2024-02-01T00:00:00Z")
	end := mustTime(t, "2024-12-31T00:00:00Z")
	// "d" is published in January and must fall outside the range.
	results, err := rssSearch.Search(ctx, "rasende", true, &start, &end, "published", 10, 0)
	if err != nil {
		t.Fatalf("ranged search: %v", err)
	}
	if got, want := itemIds(results), []string{"a", "b"}; !equal(got, want) {
		t.Errorf("ranged = %v, want %v", got, want)
	}

	descending, err := rssSearch.Search(ctx, "rasende", false, nil, nil, "-published", 10, 0)
	if err != nil {
		t.Fatalf("descending search: %v", err)
	}
	if got, want := itemIds(descending), []string{"b", "a"}; !equal(got, want) {
		t.Errorf("-published = %v, want %v", got, want)
	}

	// Offset paginates rather than re-returning the first row.
	page2, err := rssSearch.Search(ctx, "rasende", false, nil, nil, "published", 1, 1)
	if err != nil {
		t.Fatalf("paged search: %v", err)
	}
	if got, want := itemIds(page2), []string{"b"}; !equal(got, want) {
		t.Errorf("page 2 = %v, want %v", got, want)
	}
}

func TestCountsAggregate(t *testing.T) {
	rssSearch := newTestSearch(t, corpus(t))
	ctx := context.Background()

	byDay, err := rssSearch.CountByDay(ctx, "rasende", true, nil, nil)
	if err != nil {
		t.Fatalf("CountByDay: %v", err)
	}
	if len(byDay) != 3 {
		t.Fatalf("CountByDay returned %d days, want 3: %v", len(byDay), byDay)
	}
	// Oldest first: d (Jan 5), a (Mar 1), b (Mar 2).
	if !byDay[0].Timestamp.Before(byDay[1].Timestamp) {
		t.Errorf("CountByDay not ordered oldest first: %v", byDay)
	}
	for _, day := range byDay {
		if day.Count != 1 {
			t.Errorf("day %v count = %d, want 1", day.Timestamp, day.Count)
		}
	}

	bySite, err := rssSearch.CountBySite(ctx, "rasende", true)
	if err != nil {
		t.Fatalf("CountBySite: %v", err)
	}
	if len(bySite) != 1 || bySite[0].SiteId != testSite.Id || bySite[0].Count != 3 {
		t.Errorf("CountBySite = %v, want one entry with count 3", bySite)
	}
}

// Indexing happens inside the InsertItems transaction, so a re-fetch that
// re-inserts the same items (on conflict do nothing) must not duplicate index rows.
func TestReinsertDoesNotDuplicateIndexRows(t *testing.T) {
	items := corpus(t)
	rssSearch := newTestSearch(t, items)
	ctx := context.Background()

	repo := repository.NewSqliteNews(&core.AppContext{Config: rssSearch.context.Config})
	if _, err := repo.InsertItems(ctx, testSite, items); err != nil {
		t.Fatalf("re-insert: %v", err)
	}

	results, err := rssSearch.Search(ctx, "rasende", false, nil, nil, "published", 10, 0)
	if err != nil {
		t.Fatalf("search after re-insert: %v", err)
	}
	if got, want := itemIds(results), []string{"a", "b"}; !equal(got, want) {
		t.Errorf("after re-insert = %v, want %v (duplicate index rows?)", got, want)
	}
}

func TestRebuildRepopulatesIndex(t *testing.T) {
	rssSearch := newTestSearch(t, corpus(t))
	ctx := context.Background()

	if err := rssSearch.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	empty, err := rssSearch.IsEmpty(ctx)
	if err != nil {
		t.Fatalf("IsEmpty: %v", err)
	}
	if empty {
		t.Fatal("index is empty after rebuild")
	}
	results, err := rssSearch.Search(ctx, "raser", false, nil, nil, "published", 10, 0)
	if err != nil {
		t.Fatalf("search after rebuild: %v", err)
	}
	if got, want := itemIds(results), []string{"a", "b"}; !equal(got, want) {
		t.Errorf("after rebuild = %v, want %v", got, want)
	}
}
