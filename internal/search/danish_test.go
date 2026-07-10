package search

import (
	"strings"
	"testing"
)

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
			got := Analyze(tt.text)
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
		got := Analyze(word)
		if len(got) != 1 || got[0] != "ras" {
			t.Errorf("Analyze(%q) = %q, want [ras]", word, got)
		}
	}
}

func TestStemText(t *testing.T) {
	if got, want := StemText("Rasende politiker råber ad ministeren"), "ras politik råb minist"; got != want {
		t.Errorf("StemText() = %q, want %q", got, want)
	}
	if got := StemText("og i er"); got != "" {
		t.Errorf("StemText(stop words) = %q, want empty", got)
	}
}
