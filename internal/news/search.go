package news

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/search"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RssSearch queries the rss_items_fts index. Rows are written to that index by
// the repository, inside the same transaction as the rss_items insert, so the
// two can never drift apart.
//
// The index holds every language in one table, stemmed by whichever analyzer the
// publishing site's language selected. That is safe because a search is always
// scoped to one language: the tokens are matched with that language's analyzer,
// and the results are restricted to that language's sites (see siteFilter). An
// item carries no language column — it inherits its site's, which is why the
// repository is needed here.
type RssSearch struct {
	context    *core.AppContext
	repository core.NewsRepository
}

func NewRssSearch(context *core.AppContext, repository core.NewsRepository) *RssSearch {
	return &RssSearch{context: context, repository: repository}
}

// siteFilter restricts a query to the sites publishing in lang. It is the only
// thing keeping the languages apart in the shared index: without it, an English
// query could match a Danish row whose stem happened to collide.
//
// Returns false when the language has no sites, which callers must treat as
// "match nothing" — an empty IN () list is a syntax error.
func (s *RssSearch) siteFilter(ctx context.Context, lang string) (string, []any, bool, error) {
	sites, err := s.repository.GetSites(ctx)
	if err != nil {
		return "", nil, false, err
	}
	args := []any{}
	for _, site := range sites {
		if site.Language == lang {
			args = append(args, site.Id)
		}
	}
	if len(args) == 0 {
		return "", nil, false, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	return " AND i.site_id IN (" + placeholders + ")", args, true, nil
}

// siteLanguages maps site id to language, for stemming rows on the rebuild path,
// where each row may belong to a different language than the last.
func (s *RssSearch) siteLanguages(ctx context.Context) (map[int]string, error) {
	sites, err := s.repository.GetSites(ctx)
	if err != nil {
		return nil, err
	}
	languages := make(map[int]string, len(sites))
	for _, site := range sites {
		languages[site.Id] = site.Language
	}
	return languages, nil
}

var dbSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "rasende2_db_size_bytes",
	Help: "Size in bytes of the rasende2 sqlite database, which includes the search index",
})

// orderByClauses whitelists the sort values the web layer may pass through.
// bm25() returns increasingly negative scores for better matches, so ascending
// bm25 is descending relevance.
var orderByClauses = map[string]string{
	"-published": "i.published DESC",
	"published":  "i.published ASC",
	"-_score":    "bm25(rss_items_fts) ASC",
	"_score":     "bm25(rss_items_fts) DESC",
}

func orderByClause(orderBy string) string {
	if clause, ok := orderByClauses[orderBy]; ok {
		return clause
	}
	return "i.published DESC"
}

// matchExpr renders a user query as an FTS5 MATCH expression over the stemmed
// tokens held in the index, using lang's analyzer — the same one that stemmed
// the rows it will be matched against. Tokens are quoted so that FTS5 operators
// appearing in user input are treated as literal text.
//
// Reports false when the query carries no searchable terms — for example a
// query of nothing but stop words. Callers must return no results in that case;
// an empty MATCH expression is a syntax error.
func matchExpr(lang string, query string, searchContent bool) (string, bool) {
	tokens := search.Analyze(lang, query)
	if len(tokens) == 0 {
		return "", false
	}
	quoted := make([]string, len(tokens))
	for i, token := range tokens {
		quoted[i] = `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
	}
	expr := "(" + strings.Join(quoted, " OR ") + ")"
	if searchContent {
		// Unqualified: matches either column.
		return expr, true
	}
	return "{title} : " + expr, true
}

// publishedBetween appends the optional date range. published is TEXT with a
// varying number of fractional-second digits, so it is normalised by datetime()
// rather than compared lexically.
func publishedBetween(start *time.Time, end *time.Time) (string, []any) {
	clause := strings.Builder{}
	args := []any{}
	if start != nil {
		clause.WriteString(" AND datetime(i.published) >= datetime(?)")
		args = append(args, start.UTC().Format(time.RFC3339))
	}
	if end != nil {
		clause.WriteString(" AND datetime(i.published) <= datetime(?)")
		args = append(args, end.UTC().Format(time.RFC3339))
	}
	return clause.String(), args
}

// Note: FTS5 auxiliary functions such as bm25() must name the table directly,
// so rss_items_fts is never aliased. rss_items is aliased as i.
const searchFrom = " FROM rss_items_fts JOIN rss_items i ON i.id = rss_items_fts.rowid WHERE rss_items_fts MATCH ?"

func (s *RssSearch) Search(ctx context.Context, lang string, query string, searchContent bool, start *time.Time, end *time.Time, orderBy string, limit int, offset int) ([]core.RssSearchResult, error) {
	results := []core.RssSearchResult{}
	expr, ok := matchExpr(lang, query, searchContent)
	if !ok {
		return results, nil
	}
	siteClause, siteArgs, ok, err := s.siteFilter(ctx, lang)
	if err != nil || !ok {
		return results, err
	}
	dbConn, err := db.Open(s.context.Config)
	if err != nil {
		return results, err
	}
	rangeClause, args := publishedBetween(start, end)
	sqlQuery := "SELECT i.item_id, i.title, i.content, i.link, i.published, i.site_id" + searchFrom + siteClause + rangeClause +
		" ORDER BY " + orderByClause(orderBy) + " LIMIT ? OFFSET ?"
	args = append(append([]any{expr}, siteArgs...), args...)
	args = append(args, limit, offset)

	rows, err := dbConn.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return results, fmt.Errorf("error searching: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var result core.RssSearchResult
		var content, link *string
		if err := rows.Scan(&result.ItemId, &result.Title, &content, &link, &result.Published, &result.SiteId); err != nil {
			return results, fmt.Errorf("error scanning search result: %w", err)
		}
		if content != nil {
			result.Content = *content
		}
		if link != nil {
			result.Link = *link
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// CountByDay returns the number of matches per calendar day, oldest first.
func (s *RssSearch) CountByDay(ctx context.Context, lang string, query string, searchContent bool, start *time.Time, end *time.Time) ([]core.SearchQueryCount, error) {
	counts := []core.SearchQueryCount{}
	expr, ok := matchExpr(lang, query, searchContent)
	if !ok {
		return counts, nil
	}
	siteClause, siteArgs, ok, err := s.siteFilter(ctx, lang)
	if err != nil || !ok {
		return counts, err
	}
	dbConn, err := db.Open(s.context.Config)
	if err != nil {
		return counts, err
	}
	rangeClause, args := publishedBetween(start, end)
	sqlQuery := "SELECT date(i.published) AS day, count(*) AS count" + searchFrom + siteClause + rangeClause +
		" GROUP BY day ORDER BY day ASC"
	args = append(append([]any{expr}, siteArgs...), args...)

	rows, err := dbConn.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return counts, fmt.Errorf("error counting by day: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var count int
		if err := rows.Scan(&day, &count); err != nil {
			return counts, fmt.Errorf("error scanning day count: %w", err)
		}
		timestamp, err := time.Parse(time.DateOnly, day)
		if err != nil {
			slog.Warn("parsing day failed", "day", day, "error", err)
			continue
		}
		counts = append(counts, core.SearchQueryCount{Timestamp: timestamp, Count: count})
	}
	return counts, rows.Err()
}

// CountBySite returns the number of matches per site.
func (s *RssSearch) CountBySite(ctx context.Context, lang string, query string, searchContent bool) ([]core.SiteCount, error) {
	counts := []core.SiteCount{}
	expr, ok := matchExpr(lang, query, searchContent)
	if !ok {
		return counts, nil
	}
	siteClause, siteArgs, ok, err := s.siteFilter(ctx, lang)
	if err != nil || !ok {
		return counts, err
	}
	dbConn, err := db.Open(s.context.Config)
	if err != nil {
		return counts, err
	}
	sqlQuery := "SELECT i.site_id, count(*) AS count" + searchFrom + siteClause + " GROUP BY i.site_id"

	rows, err := dbConn.QueryContext(ctx, sqlQuery, append([]any{expr}, siteArgs...)...)
	if err != nil {
		return counts, fmt.Errorf("error counting by site: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteCount core.SiteCount
		if err := rows.Scan(&siteCount.SiteId, &siteCount.Count); err != nil {
			return counts, fmt.Errorf("error scanning site count: %w", err)
		}
		counts = append(counts, siteCount)
	}
	return counts, rows.Err()
}

const rebuildBatchSize = 5000

// Rebuild discards the index and reindexes every rss_item. It is the recovery
// path after an analyzer change, since the stemmed tokens on disk are only
// meaningful relative to the analyzer that produced them.
//
// Each row is re-stemmed in the language of the site that published it, not in
// one language for the whole table: rebuilding everything as Danish would leave
// the English edition matching nothing, silently.
func (s *RssSearch) Rebuild(ctx context.Context) error {
	dbConn, err := db.Open(s.context.Config)
	if err != nil {
		return err
	}
	languages, err := s.siteLanguages(ctx)
	if err != nil {
		return err
	}
	startTime := time.Now()
	slog.Info("rebuilding search index")

	// 'delete-all' is the FTS5 command for emptying a contentless table.
	if _, err := dbConn.ExecContext(ctx, "INSERT INTO rss_items_fts(rss_items_fts) VALUES('delete-all')"); err != nil {
		return fmt.Errorf("error clearing search index: %w", err)
	}

	count, skipped := 0, 0
	lastId := int64(0)
	for {
		indexed, skippedInBatch, nextId, err := s.indexBatch(ctx, dbConn, languages, lastId)
		if err != nil {
			return err
		}
		if indexed == 0 && skippedInBatch == 0 {
			break
		}
		count += indexed
		skipped += skippedInBatch
		lastId = nextId
		slog.Debug("indexed documents", "count", count, "duration_s", time.Since(startTime).Seconds())
	}
	slog.Info("rebuilt search index",
		"documents", count,
		"duration_s", time.Since(startTime).Seconds(),
		"skipped_no_known_site", skipped)
	s.RefreshMetrics()
	return nil
}

// indexBatch indexes up to rebuildBatchSize rows with id > afterId, keyset
// paginated so the scan cost does not grow with the offset. It returns the
// number indexed, the number skipped, and the id to resume from.
//
// site_id is nullable (it was added by a later migration), and a row may also
// name a site that no longer exists in rss.json. Such a row has no language, so
// it cannot be stemmed — and siteFilter already excludes it from every search,
// so indexing it would only add tokens nothing can ever match. Skip it, and
// report how many, rather than guessing at a language.
func (s *RssSearch) indexBatch(ctx context.Context, dbConn *sql.DB, languages map[int]string, afterId int64) (int, int, int64, error) {
	rows, err := dbConn.QueryContext(ctx,
		"SELECT id, title, content, site_id FROM rss_items WHERE id > ? ORDER BY id ASC LIMIT ?", afterId, rebuildBatchSize)
	if err != nil {
		return 0, 0, afterId, fmt.Errorf("error reading items to index: %w", err)
	}
	type doc struct {
		id      int64
		title   string
		content string
		lang    string
	}
	docs := []doc{}
	skipped := 0
	lastId := afterId
	for rows.Next() {
		var d doc
		var content *string
		var siteId *int
		if err := rows.Scan(&d.id, &d.title, &content, &siteId); err != nil {
			rows.Close()
			return 0, 0, afterId, fmt.Errorf("error scanning item to index: %w", err)
		}
		lastId = d.id
		if content != nil {
			d.content = *content
		}
		if siteId == nil {
			skipped++
			continue
		}
		lang, ok := languages[*siteId]
		if !ok {
			skipped++
			continue
		}
		d.lang = lang
		docs = append(docs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, afterId, err
	}
	if len(docs) == 0 {
		return 0, skipped, lastId, nil
	}

	tx, err := dbConn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, afterId, fmt.Errorf("failed to begin index tx: %w", err)
	}
	for _, d := range docs {
		if _, err := tx.ExecContext(ctx, search.InsertFtsSQL, d.id,
			search.StemText(d.lang, d.title), search.StemText(d.lang, d.content)); err != nil {
			tx.Rollback()
			return 0, 0, afterId, fmt.Errorf("error indexing item %d: %w", d.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, afterId, fmt.Errorf("failed to commit index tx: %w", err)
	}
	return len(docs), skipped, lastId, nil
}

// IsEmpty reports whether the index holds no documents, which is the signal to
// backfill it on startup.
func (s *RssSearch) IsEmpty(ctx context.Context) (bool, error) {
	dbConn, err := db.Open(s.context.Config)
	if err != nil {
		return false, err
	}
	var count int
	if err := dbConn.QueryRowContext(ctx, "SELECT count(*) FROM rss_items_fts").Scan(&count); err != nil {
		return false, fmt.Errorf("error counting search index: %w", err)
	}
	return count == 0, nil
}

func (s *RssSearch) RefreshMetrics() {
	stat, err := os.Stat(s.context.Config.DbConnStr)
	if err != nil {
		slog.Error("stat database failed", "path", s.context.Config.DbConnStr, "error", err)
		return
	}
	dbSizeGauge.Set(float64(stat.Size()))
}
