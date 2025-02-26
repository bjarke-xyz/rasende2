package core

import "testing"

func TestNewsBlockedTitlePattern(t *testing.T) {
	t.Parallel()

	okTitlePatterns := []string{".+– følg med her$"}
	errTitlePatterns := []string{".[+– følg$"}

	var tests = []struct {
		title                string
		expectedResult       bool
		expectedError        bool
		blockedTitlePatterns []string
	}{
		{"Bla bla bla – følg med her", true, false, okTitlePatterns},
		{"– følg med her", false, false, okTitlePatterns},
		{"Bla bla bla – følg med her bla bla", false, false, okTitlePatterns},
		{"Bla bla bla", false, false, okTitlePatterns},
		{"Bla bla bla", false, true, errTitlePatterns},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			newsSite := NewsSite{
				Name:                 "Test site",
				Urls:                 []string{"https://example.org"},
				Description:          "A test site",
				DescriptionEn:        "A test site",
				Id:                   1,
				Disabled:             false,
				ArticleHasContent:    false,
				BlockedTitlePatterns: tt.blockedTitlePatterns,
			}
			result, err := newsSite.IsBlockedTitle(tt.title)
			if tt.expectedError && err == nil {
				t.Errorf("got nil err, but expected err")
			}
			if result != tt.expectedResult {
				t.Errorf("got %v, want %v", result, tt.expectedResult)
			}
		})
	}
}
