// Package search provides per-language text analysis for the SQLite FTS5 index.
//
// SQLite ships no Danish stemmer, so the same pipeline runs in Go on both sides
// of the index: text is stemmed before it is written to rss_items_fts, and a
// query is stemmed before it is matched against it. Only the resulting tokens
// ever reach FTS5, whose unicode61 tokenizer then just splits them on
// whitespace. This is what lets a search for "raser" find "rasende".
//
// The Danish pipeline reproduces bleve's "da" analyzer, which this replaced:
// UAX#29 word segmentation, lowercase, stop words, Snowball stemmer. English
// follows the same shape with its own stop words and stemmer.
//
// Because the index holds stems rather than words, the tokens on disk are only
// meaningful relative to the analyzer that wrote them. A row indexed as Danish
// must be queried as Danish. Rows carry no language of their own: the language
// is a property of the site that published them, and searches are scoped to one
// language's sites (see internal/news/search.go).
package search

import (
	"fmt"
	"strings"

	"github.com/blevesearch/segment"
	"github.com/blevesearch/snowballstem"
)

// analyzer is the part of the pipeline that differs between languages. The
// segmentation and lowercasing around it are shared, so that adding a language
// cannot perturb the token stream of an existing one — the stems already on disk
// depend on it staying byte-identical.
type analyzer struct {
	stopWords map[string]struct{}
	stem      func(*snowballstem.Env) bool
}

var analyzers = map[string]analyzer{
	"da": {stopWords: danishStopWords, stem: danishStem},
	"en": {stopWords: englishStopWords, stem: englishStem},
}

// Supported reports whether lang has an analyzer. Site languages are checked
// against this at startup so that an unsupported one fails the boot rather than
// panicking later, in a background fetch or mid-request.
func Supported(lang string) bool {
	_, ok := analyzers[lang]
	return ok
}

// Analyze splits text into lowercased, stop-word-filtered, stemmed tokens for
// lang. It returns an empty slice when the input carries no searchable terms,
// which callers must treat as "match nothing" rather than building an empty
// query.
//
// An unknown lang panics. It cannot come from user input — it is either a site's
// configured language, already validated by Supported at startup, or one of the
// editions in internal/lang — so reaching here with one is a bug, and silently
// falling back to some default language would corrupt the index instead.
func Analyze(lang string, text string) []string {
	a, ok := analyzers[lang]
	if !ok {
		panic(fmt.Sprintf("search: no analyzer for language %q", lang))
	}
	if text == "" {
		return nil
	}
	tokens := []string{}
	seg := segment.NewWordSegmenterDirect([]byte(text))
	for seg.Segment() {
		if seg.Type() == segment.None {
			continue
		}
		word := strings.ToLower(string(seg.Bytes()))
		if _, stop := a.stopWords[word]; stop {
			continue
		}
		env := snowballstem.NewEnv(word)
		a.stem(env)
		tokens = append(tokens, env.Current())
	}
	return tokens
}

// StemText renders text as the space-joined token stream stored in the FTS5 index.
func StemText(lang string, text string) string {
	return strings.Join(Analyze(lang, text), " ")
}

// InsertFtsSQL indexes one rss_item. Its rowid must be rss_items.id, and its
// title and content must have been passed through StemText with the language of
// the site that published it. The repository runs this inside the same
// transaction as the rss_items insert so that the index cannot drift;
// RssSearch.Rebuild runs it to backfill.
const InsertFtsSQL = "INSERT INTO rss_items_fts(rowid, title, content) VALUES (?, ?, ?)"
