package repository

import (
	"context"
	"testing"
)

func TestGetSites(t *testing.T) {
	t.Parallel()

	// NOTE: this is not a usable repository. It is only used to call GetSites, which loads a json file
	sqliteNewsRepository := NewSqliteNews(nil)

	sites, err := sqliteNewsRepository.GetSites(context.Background())
	if err != nil {
		t.Errorf("error getting sites: %v", err)
	}
	for _, site := range sites {
		if len(site.BlockedTitlePatterns) > 0 {
			t.Run(site.Name, func(t *testing.T) {
				_, err := site.IsBlockedTitle("test")
				if err != nil {
					t.Errorf("error in checking blocked title for site: %v", err)
				}
			})
		}
	}
}
