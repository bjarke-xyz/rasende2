package lang

import (
	"slices"
	"testing"

	"github.com/bjarke-xyz/rasende2/internal/search"
)

// The repository admits a site if its language is an edition, and everything
// downstream then stems that site's items on trust. So an edition without an
// analyzer would panic on the first item fetched, in a background job. Tie the
// two together here instead.
func TestEveryEditionHasAnAnalyzer(t *testing.T) {
	for _, l := range All {
		if !search.Supported(string(l.Code)) {
			t.Errorf("edition %q has no analyzer in internal/search", l.Code)
		}
	}
}

// Every edition must define every key. A key present in one catalog and missing
// from another is invisible until someone loads that page in that language, and
// T() would render the raw key into the page.
func TestCatalogsHaveIdenticalKeys(t *testing.T) {
	want := All[0].Keys()
	slices.Sort(want)
	for _, l := range All[1:] {
		got := l.Keys()
		slices.Sort(got)
		for _, key := range want {
			if !slices.Contains(got, key) {
				t.Errorf("catalog %q is missing key %q, which %q has", l.Code, key, All[0].Code)
			}
		}
		for _, key := range got {
			if !slices.Contains(want, key) {
				t.Errorf("catalog %q has key %q, which %q does not", l.Code, key, All[0].Code)
			}
		}
	}
}

func TestT(t *testing.T) {
	da := MustGet(Da)
	if got, want := da.T("nav.search"), "Søg"; got != want {
		t.Errorf("T(nav.search) = %q, want %q", got, want)
	}
	if got, want := da.T("chart.line.datasetQuery", "øl"), "Antal 'øl'"; got != want {
		t.Errorf("T with args = %q, want %q", got, want)
	}
	// A missing key renders as itself, so the gap is visible rather than blank.
	if got, want := da.T("no.such.key"), "no.such.key"; got != want {
		t.Errorf("T(missing) = %q, want %q", got, want)
	}
}

func TestGet(t *testing.T) {
	for _, code := range []string{"da", "en"} {
		if _, ok := Get(code); !ok {
			t.Errorf("Get(%q) failed", code)
		}
	}
	for _, code := range []string{"", "sv", "DA", "robots.txt"} {
		if _, ok := Get(code); ok {
			t.Errorf("Get(%q) succeeded, want failure", code)
		}
	}
}

// The premise of each edition: the default query must be a word whose
// inflections collapse to a single stem, or searching it finds only the exact
// spelling. internal/search asserts the collapse itself; this asserts we did not
// quietly blank the word out.
func TestEveryEditionHasAPremiseWord(t *testing.T) {
	for _, l := range All {
		if l.DefaultQuery == "" {
			t.Errorf("edition %q has no DefaultQuery", l.Code)
		}
		if l.Name == "" {
			t.Errorf("edition %q has no Name; the LLM prompts interpolate it", l.Code)
		}
		if l.Endonym == "" {
			t.Errorf("edition %q has no Endonym; it would be an unlabelled language switcher link", l.Code)
		}
	}
}
