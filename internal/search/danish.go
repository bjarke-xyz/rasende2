// Package search provides Danish text analysis for the SQLite FTS5 index.
//
// SQLite ships no Danish stemmer, so the same pipeline runs in Go on both sides
// of the index: text is stemmed before it is written to rss_items_fts, and a
// query is stemmed before it is matched against it. Only the resulting tokens
// ever reach FTS5, whose unicode61 tokenizer then just splits them on
// whitespace. This is what lets a search for "raser" find "rasende".
//
// The pipeline reproduces bleve's "da" analyzer, which this replaced:
// UAX#29 word segmentation, lowercase, Danish stop words, Snowball Danish stemmer.
package search

import (
	"strings"

	"github.com/blevesearch/segment"
	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/danish"
)

// stopWords is the Snowball Danish stop word list, as used by bleve's da analyzer.
var stopWords = map[string]struct{}{}

func init() {
	words := []string{
		"ad", "af", "alle", "alt", "anden", "at", "blev", "blive",
		"bliver", "da", "de", "dem", "den", "denne", "der", "deres",
		"det", "dette", "dig", "din", "disse", "dog", "du", "efter",
		"eller", "en", "end", "er", "et", "for", "fra", "ham",
		"han", "hans", "har", "havde", "have", "hende", "hendes", "her",
		"hos", "hun", "hvad", "hvis", "hvor", "i", "ikke", "ind",
		"jeg", "jer", "jo", "kunne", "man", "mange", "med", "meget",
		"men", "mig", "min", "mine", "mit", "mod", "ned", "noget",
		"nogle", "nu", "når", "og", "også", "om", "op", "os",
		"over", "på", "selv", "sig", "sin", "sine", "sit", "skal",
		"skulle", "som", "sådan", "thi", "til", "ud", "under", "var",
		"vi", "vil", "ville", "vor", "være", "været",
	}
	for _, w := range words {
		stopWords[w] = struct{}{}
	}
}

// Analyze splits text into lowercased, stop-word-filtered, Danish-stemmed tokens.
// It returns an empty slice when the input carries no searchable terms, which
// callers must treat as "match nothing" rather than building an empty query.
func Analyze(text string) []string {
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
		if _, stop := stopWords[word]; stop {
			continue
		}
		env := snowballstem.NewEnv(word)
		danish.Stem(env)
		tokens = append(tokens, env.Current())
	}
	return tokens
}

// StemText renders text as the space-joined token stream stored in the FTS5 index.
func StemText(text string) string {
	return strings.Join(Analyze(text), " ")
}

// InsertFtsSQL indexes one rss_item. Its rowid must be rss_items.id, and its
// title and content must have been passed through StemText. The repository runs
// this inside the same transaction as the rss_items insert so that the index
// cannot drift; RssSearch.Rebuild runs it to backfill.
const InsertFtsSQL = "INSERT INTO rss_items_fts(rowid, title, content) VALUES (?, ?, ?)"
