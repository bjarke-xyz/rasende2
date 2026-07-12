package search

import (
	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/english"
)

func englishStem(env *snowballstem.Env) bool { return english.Stem(env) }

// englishStopWords is the Snowball English stop word list, the counterpart of
// danishStopWords. Note it does not contain the English edition's premise word
// or any of its inflections, which all stem to "outrag".
var englishStopWords = wordSet(
	"a", "about", "above", "after", "again", "against", "all", "am",
	"an", "and", "any", "are", "as", "at", "be", "because",
	"been", "before", "being", "below", "between", "both", "but", "by",
	"can", "cannot", "could", "did", "do", "does", "doing", "down",
	"during", "each", "few", "for", "from", "further", "had", "has",
	"have", "having", "he", "her", "here", "hers", "herself", "him",
	"himself", "his", "how", "i", "if", "in", "into", "is",
	"it", "its", "itself", "let", "me", "more", "most", "my",
	"myself", "no", "nor", "not", "of", "off", "on", "once",
	"only", "or", "other", "ought", "our", "ours", "ourselves", "out",
	"over", "own", "same", "she", "should", "so", "some", "such",
	"than", "that", "the", "their", "theirs", "them", "themselves", "then",
	"there", "these", "they", "this", "those", "through", "to", "too",
	"under", "until", "up", "very", "was", "we", "were", "what",
	"when", "where", "which", "while", "who", "whom", "why", "with",
	"would", "you", "your", "yours", "yourself", "yourselves",
)
