package pkg

import (
	"strings"
	"unicode/utf8"
)

// quotePairs are the quotation marks a model reaches for when it decides a title
// is a quoted string. Danish uses the low-high form (»...« and „...“), English
// the straight and curly ones, and the model picks whichever suits the language
// it was told to write in.
var quotePairs = map[rune]rune{
	'"':  '"',
	'\'': '\'',
	'“':  '”', // “ ”
	'‘':  '’', // ‘ ’
	'„':  '“', // „ “
	'»':  '«', // » «
	'«':  '»', // « »
}

// CleanGeneratedTitle strips what a language model wraps around a title even
// when told not to: surrounding whitespace, a list marker, and the quotation
// marks it adds when it treats the title as a quoted string.
//
// Only a *matched* pair is removed, so a headline that merely ends in a quote —
// `Mads raser på Viaplay: 'Simpelthen ikke i orden'` — keeps it. A headline that
// is nothing but a quotation does lose its outer marks; that is the accepted
// cost, and it is rarer than the artifact this removes.
func CleanGeneratedTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimLeft(title, "-*• \t")

	// Repeat: a model that quotes will occasionally quote twice.
	for utf8.RuneCountInString(title) > 1 {
		first, firstSize := utf8.DecodeRuneInString(title)
		closer, isQuote := quotePairs[first]
		if !isQuote {
			break
		}
		last, lastSize := utf8.DecodeLastRuneInString(title)
		if last != closer {
			break
		}
		title = strings.TrimSpace(title[firstSize : len(title)-lastSize])
	}
	return title
}
