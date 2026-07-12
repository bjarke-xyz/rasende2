package pkg

import "testing"

func TestCleanGeneratedTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{"plain", "Minister raser over nye tal", "Minister raser over nye tal"},
		{"leading space", " Minister raser over nye tal", "Minister raser over nye tal"},

		// What the model actually emitted while the prompt told it to "start each
		// line with a space (' ')": it copied the quotes out of the example.
		{"the artifact", "' Global Warming Just A Hoax'", "Global Warming Just A Hoax"},

		{"double quotes", `"Yankees Win World Series"`, "Yankees Win World Series"},
		{"curly quotes", "“Yankees Win World Series”", "Yankees Win World Series"},
		{"danish quotes", "»Minister raser«", "Minister raser"},
		{"quoted twice", `"'Minister raser'"`, "Minister raser"},
		{"list marker", "- Minister raser over nye tal", "Minister raser over nye tal"},
		{"numbered", "1. Minister raser", "1. Minister raser"}, // digits are not a marker we strip

		// The case a careless strip would ruin: a tabloid headline whose *quote* is
		// part of the headline. The opening character is not a quote, so nothing is
		// stripped and the closing one survives.
		{"trailing quote is part of the headline", "Mads raser på Viaplay: 'Simpelthen ikke i orden'", "Mads raser på Viaplay: 'Simpelthen ikke i orden'"},

		// Unmatched quotes are left alone rather than half-stripped.
		{"unmatched", `"Minister raser`, `"Minister raser`},

		{"empty", "", ""},
		{"only whitespace", "   ", ""},
		{"only a quote", `"`, `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanGeneratedTitle(tt.title); got != tt.want {
				t.Errorf("CleanGeneratedTitle(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}
