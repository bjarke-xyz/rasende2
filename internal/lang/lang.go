// Package lang defines the site's language editions.
//
// An edition is more than a translation. The app exists to count how often the
// press reaches for one tired word, so each edition needs its own cliché:
// Danish headlines say "rasende", English ones say "outrage". DefaultQuery is
// that word, and it is what the index page counts and the charts are titled
// after.
//
// The word cannot simply be translated. It has to survive its language's
// stemmer, so that every inflection collapses to one token and searching any of
// them finds all of them — "rasende"/"raser"/"rase" all stem to "ras", and
// "outrage"/"outraged"/"outrageous" all stem to "outrag". "Furious" would have
// been the literal translation and the wrong choice: it stems apart from "fury".
// See internal/search.
package lang

import (
	"fmt"

	"github.com/xeonx/timeago"
)

type Code string

const (
	Da Code = "da"
	En Code = "en"
)

// Default is the edition served to a visitor who asked for no particular one.
const Default = Da

type Lang struct {
	Code Code

	// Name is the language in English, interpolated into the LLM prompts
	// ("Write the article in Danish."). The prompts themselves stay English
	// whatever the edition; only the language of their output changes.
	Name string

	// Endonym is the language's name in itself — "Dansk", "English". It labels
	// this edition's link in the language switcher, and it is deliberately not a
	// catalog string: a switcher entry is read by someone who wants *that*
	// language, so it must be legible to them and not to the page they are on.
	// It also means a third edition needs no new key in every other catalog.
	Endonym string

	// DefaultQuery is the edition's cliché: the word the index page counts.
	DefaultQuery string

	// TimeAgo formats "3 hours ago" on the index page.
	TimeAgo timeago.Config

	msgs map[string]string
}

// T looks up a message. A missing key returns the key itself rather than an
// empty string, so a gap shows up in the page instead of silently blanking it —
// though TestCatalogsCoverTemplates should have caught it first.
func (l Lang) T(key string, args ...any) string {
	msg, ok := l.msgs[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Keys returns the catalog's keys, for the parity test.
func (l Lang) Keys() []string {
	keys := make([]string, 0, len(l.msgs))
	for k := range l.msgs {
		keys = append(keys, k)
	}
	return keys
}

var danish = Lang{
	Code:         Da,
	Name:         "Danish",
	Endonym:      "Dansk",
	DefaultQuery: "rasende",
	TimeAgo:      danishTimeAgo,
	msgs:         daMsgs,
}

var english = Lang{
	Code:         En,
	Name:         "English",
	Endonym:      "English",
	DefaultQuery: "outrage",
	// timeago.English already carries the same Max (73h) and DefaultLayout
	// ("2006-01-02") the Danish config sets, so the two editions agree on when
	// to stop saying "ago" and print a date instead.
	TimeAgo: timeago.English,
	msgs:    enMsgs,
}

// All is every edition, in the order they appear in the language switcher.
var All = []Lang{danish, english}

// Get resolves a language code. It is the only way a request's language is
// decided, so an unknown code must not fall back silently — the router turns a
// miss into a 404.
func Get(code string) (Lang, bool) {
	for _, l := range All {
		if string(l.Code) == code {
			return l, true
		}
	}
	return Lang{}, false
}

// MustGet is Get for a code that is known at compile time or already validated.
func MustGet(code Code) Lang {
	l, ok := Get(string(code))
	if !ok {
		panic(fmt.Sprintf("lang: unknown language %q", code))
	}
	return l
}
