package search

import (
	"strings"
	"testing"
)

// The expectations below are golden: the Danish rows already in rss_items_fts
// were stemmed by exactly this pipeline. If a change here alters the tokens, the
// stems on disk become stale and search quietly stops matching, with nothing to
// signal it. Treat a failure as "the index needs rebuilding", not "fix the test".
func TestAnalyze(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"empty", "", nil},
		{"stop words only", "og i er det", []string{}},
		{"lowercases", "RASENDE", []string{"ras"}},
		{"strips punctuation", "Rasende!!! politiker...", []string{"ras", "politik"}},
		{"keeps danish letters", "øl ost rødgrød fløde Ærø", []string{"øl", "ost", "rødgrød", "flød", "ærø"}},
		{"drops stop words, stems rest", "En rasende mand på gaden og i huset", []string{"ras", "mand", "gad", "hus"}},
		{"stems noun inflections", "biler bilen bil bilerne", []string{"bil", "bil", "bil", "bil"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Analyze("da", tt.text)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("Analyze(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// The whole point of the Go-side analyzer: inflections of "rase" must collapse
// to one token so that searching any of them finds all of them.
func TestAnalyzeCollapsesRasendeInflections(t *testing.T) {
	for _, word := range []string{"rasende", "raser", "rase", "Rasende", "RASER"} {
		got := Analyze("da", word)
		if len(got) != 1 || got[0] != "ras" {
			t.Errorf("Analyze(%q) = %q, want [ras]", word, got)
		}
	}
}

// The English edition's premise word has to collapse the same way "rasende"
// does, or searching it finds only the exact spelling. This is why the word is
// "outrage" and not "furious": fury/furious stem apart, outrage/outraged/
// outrages/outrageous do not.
func TestAnalyzeCollapsesOutrageInflections(t *testing.T) {
	for _, word := range []string{"outrage", "outraged", "outrages", "outrageous", "Outrage", "OUTRAGED"} {
		got := Analyze("en", word)
		if len(got) != 1 || got[0] != "outrag" {
			t.Errorf("Analyze(%q) = %q, want [outrag]", word, got)
		}
	}
}

func TestAnalyzeEnglish(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"empty", "", nil},
		{"stop words only", "the and is of", []string{}},
		{"drops stop words, stems rest", "The minister is outraged at the bankers", []string{"minist", "outrag", "banker"}},
		{"strips punctuation", "Outrage!!! as minister...", []string{"outrag", "minist"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Analyze("en", tt.text)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("Analyze(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestStemText(t *testing.T) {
	if got, want := StemText("da", "Rasende politiker råber ad ministeren"), "ras politik råb minist"; got != want {
		t.Errorf("StemText() = %q, want %q", got, want)
	}
	if got := StemText("da", "og i er"); got != "" {
		t.Errorf("StemText(stop words) = %q, want empty", got)
	}
}

// A row indexed as Danish must not be found by an English query. The site filter
// in internal/news/search.go is what enforces that, but it only works because the
// two analyzers really do produce different tokens for the same text.
func TestAnalyzersDisagree(t *testing.T) {
	const text = "Rasende politiker"
	if da, en := StemText("da", text), StemText("en", text); da == en {
		t.Errorf("da and en produced the same tokens %q; the site filter is the only thing keeping the languages apart", da)
	}
}

func TestSupported(t *testing.T) {
	for _, lang := range []string{"da", "en"} {
		if !Supported(lang) {
			t.Errorf("Supported(%q) = false, want true", lang)
		}
	}
	for _, lang := range []string{"", "sv", "DA"} {
		if Supported(lang) {
			t.Errorf("Supported(%q) = true, want false", lang)
		}
	}
}

// An unknown language must not silently fall back to some default: that would
// write the wrong stems into the index, which nothing downstream would catch.
func TestAnalyzeUnknownLanguagePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Analyze with an unknown language returned instead of panicking")
		}
	}()
	Analyze("sv", "rasande politiker")
}
