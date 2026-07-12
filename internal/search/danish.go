package search

import (
	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/danish"
)

func danishStem(env *snowballstem.Env) bool { return danish.Stem(env) }

// danishStopWords is the Snowball Danish stop word list, as used by bleve's da
// analyzer. The Danish index on disk was built with exactly this list; changing
// it invalidates every stored row (see RebuildSearchIndex).
var danishStopWords = wordSet(
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
)

func wordSet(words ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}
