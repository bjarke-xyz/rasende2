package repository

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// content and link are nullable in rss_items, but plain strings on RssItemDto.
// Scanning a NULL through a *string and defaulting to "" is what keeps a NULL row
// from failing the whole query.
func TestScanRssItemHandlesNulls(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE rss_items(
		item_id TEXT, site_name TEXT, title TEXT, content TEXT, link TEXT,
		published TIMESTAMP, inserted_at TIMESTAMP, site_id INTEGER);
		INSERT INTO rss_items VALUES('a','Testmedie','Rasende',NULL,NULL,'2024-03-01 10:00:00+00:00',NULL,1);`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	item, err := scanRssItem(db.QueryRow("SELECT " + rssItemColumns + " FROM rss_items"))
	if err != nil {
		t.Fatalf("scanRssItem on NULL content/link: %v", err)
	}
	if item.Content != "" || item.Link != "" {
		t.Errorf("NULL content/link = %q/%q, want empty strings", item.Content, item.Link)
	}
	if item.Title != "Rasende" || item.ItemId != "a" || item.SiteId != 1 {
		t.Errorf("unexpected item: %+v", item)
	}
	if item.InsertedAt != nil {
		t.Errorf("NULL inserted_at = %v, want nil", item.InsertedAt)
	}
}

// published and site_id are nullable in fake_news.
func TestScanFakeNewsHandlesNulls(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE fake_news(
		site_name TEXT, title TEXT, content TEXT, published TIMESTAMP, site_id INTEGER,
		img_url TEXT, highlighted BOOLEAN NOT NULL DEFAULT 0, votes INT NOT NULL DEFAULT 0, external_id TEXT);
		INSERT INTO fake_news VALUES('TV2','Titel','Indhold',NULL,NULL,NULL,1,3,NULL);`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	fakeNews, err := scanFakeNews(db.QueryRow("SELECT " + fakeNewsColumns + " FROM fake_news"))
	if err != nil {
		t.Fatalf("scanFakeNews on NULL published/site_id: %v", err)
	}
	if !fakeNews.Published.IsZero() || fakeNews.SiteId != 0 {
		t.Errorf("NULL published/site_id = %v/%d, want zero values", fakeNews.Published, fakeNews.SiteId)
	}
	if fakeNews.ImageUrl != nil || fakeNews.ExternalId != nil {
		t.Errorf("NULL img_url/external_id should stay nil, got %v/%v", fakeNews.ImageUrl, fakeNews.ExternalId)
	}
	if !fakeNews.Highlighted || fakeNews.Votes != 3 {
		t.Errorf("unexpected fake news: %+v", fakeNews)
	}
}

func TestPlaceholders(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{{1, "?"}, {3, "?,?,?"}} {
		if got := placeholders(tt.n); got != tt.want {
			t.Errorf("placeholders(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
