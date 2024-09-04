package core

import (
	"context"
	"crypto/md5"
	"fmt"
	"time"
)

type NewsRepository interface {
	GetSites(ctx context.Context) ([]NewsSite, error)
	GetSiteNames(ctx context.Context) ([]string, error)
	GetRecentItems(ctx context.Context, siteId int, limit int, insertAtOffset *time.Time) ([]RssItemDto, error)
	GetRecentItemIds(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time, maxLookBack *time.Time) ([]string, *time.Time, error)
	GetItemsByIds(ctx context.Context, itemIds []string) ([]RssItemDto, error)
	GetSiteCountByItemIds(ctx context.Context, allItemIds []string) ([]SiteCount, error)
	GetExistingItemsByIds(ctx context.Context, itemIds []string) (map[string]any, error)
	GetArticleCounts(ctx context.Context) (map[int]int, error)
	InsertItems(ctx context.Context, newsSite NewsSite, items []RssItemDto) (int, error)
	EnrichSiteCountWithSiteNames(ctx context.Context, siteCounts []SiteCount)
	EnrichRssSearchResultWithSiteNames(ctx context.Context, rssSearchResults []RssSearchResult)

	GetRecentFakeNews(ctx context.Context, limit int, publishedAfter *time.Time) ([]FakeNewsDto, error)
	GetPopularFakeNews(ctx context.Context, limit int, publishedAfter *time.Time, votes int) ([]FakeNewsDto, error)
	GetFakeNews(ctx context.Context, siteId int, title string) (*FakeNewsDto, error)
	CreateFakeNews(ctx context.Context, siteId int, title string) error
	UpdateFakeNews(ctx context.Context, siteId int, title string, content string) error
	SetFakeNewsImgUrl(ctx context.Context, siteId int, title string, imgUrl string) error
	SetFakeNewsHighlighted(ctx context.Context, siteId int, title string, highlighted bool) error
	ResetFakeNewsContent(ctx context.Context, siteId int, title string) error
	VoteFakeNews(ctx context.Context, siteId int, title string, votes int) (int, error)
}

type NewsService interface {
	Initialise(ctx context.Context)
	Dispose()
	GetIndexPageData(ctx context.Context, nocache bool) (*IndexPageData, error)
	GetChartData(ctx context.Context, query string) (ChartsResult, error)
	GetSiteNames(ctx context.Context) ([]string, error)
	GetSiteInfos(ctx context.Context) ([]NewsSite, error)
	GetSiteInfo(ctx context.Context, siteName string) (*NewsSite, error)
	GetSiteInfoById(ctx context.Context, id int) (*NewsSite, error)
	SearchItems(ctx context.Context, query string, searchContent bool, offset int, limit int, orderBy string) ([]RssSearchResult, error)
	GetItemCountForSearchQuery(ctx context.Context, query string, searchContent bool, start *time.Time, end *time.Time, orderBy string) ([]SearchQueryCount, error)
	GetSiteCountForSearchQuery(ctx context.Context, query string, searchContent bool) ([]SiteCount, error)
	GetRecentTitles(ctx context.Context, siteInfo NewsSite, limit int, shuffle bool) ([]string, error)
	GetRecentItems(ctx context.Context, siteId int, limit int, insertedAtOffset *time.Time) ([]RssItemDto, error)
	AddMissingItemsToSearchIndexAndLogError(ctx context.Context, maxLookBack *time.Time)

	GetPopularFakeNews(ctx context.Context, limit int, publishedAfter *time.Time, votes int) ([]FakeNewsDto, error)
	GetRecentFakeNews(ctx context.Context, limit int, publishedAfter *time.Time) ([]FakeNewsDto, error)
	GetFakeNews(ctx context.Context, siteId int, title string) (*FakeNewsDto, error)
	CreateFakeNews(ctx context.Context, siteId int, title string) error
	UpdateFakeNews(ctx context.Context, siteId int, title string, content string) error
	SetFakeNewsImgUrl(ctx context.Context, siteId int, title string, imgUrl string) error
	SetFakeNewsHighlighted(ctx context.Context, siteId int, title string, highlighted bool) error
	ResetFakeNewsContent(ctx context.Context, siteId int, title string) error
	VoteFakeNews(ctx context.Context, siteId int, title string, votes int) (int, error)
	CleanUpFakeNewsAndLogError(ctx context.Context)
	CleanUpFakeNews(ctx context.Context) error

	FetchAndSaveNewItems(ctx context.Context) error
	RefreshMetrics(ctx context.Context) error
	BackupDbAndLogError(ctx context.Context) error
	NotifyBackupDbError(ctx context.Context, err error) error
	BackupDb(ctx context.Context) error
}

type IndexPageData struct {
	SearchResult *SearchResult
	ChartsResult *ChartsResult
}
type SiteCount struct {
	SiteId   int    `json:"siteId"`
	SiteName string `json:"siteName"`
	Count    int    `json:"count"`
}

type SearchQueryCount struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int       `json:"count"`
}

type RssSearchResult struct {
	ItemId    string    `json:"itemId"`
	SiteName  string    `json:"siteName"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Link      string    `json:"link"`
	Published time.Time `json:"published"`
	SiteId    int       `json:"siteId"`
}

type NewsSite struct {
	Name              string   `json:"name"`
	Urls              []string `json:"urls"`
	Description       string   `json:"description"`
	DescriptionEn     string   `json:"descriptionEn"`
	Id                int      `json:"id"`
	Disabled          bool     `json:"disabled"`
	ArticleHasContent bool     `json:"articleHasContent"`
}

type RssItemDto struct {
	ItemId     string     `db:"item_id" json:"itemId"`
	SiteName   string     `db:"site_name" json:"siteName"`
	Title      string     `db:"title" json:"title"`
	Content    string     `db:"content" json:"content"`
	Link       string     `db:"link" json:"link"`
	Published  time.Time  `db:"published" json:"published"`
	InsertedAt *time.Time `db:"inserted_at" json:"insertedAt"`
	SiteId     int        `db:"site_id" json:"siteId"`
}

type FakeNewsDto struct {
	SiteName    string    `db:"site_name" json:"siteName"`
	Title       string    `db:"title" json:"title"`
	Content     string    `db:"content" json:"content"`
	Published   time.Time `db:"published" json:"published"`
	SiteId      int       `db:"site_id" json:"siteId"`
	ImageUrl    *string   `db:"img_url" json:"imageUrl"`
	Highlighted bool      `db:"highlighted" json:"highlighted"`
	Votes       int       `db:"votes" json:"votes"`
}

func (fn FakeNewsDto) Slug() string {
	return fmt.Sprintf("%v-%v-%v", fn.SiteId, fn.Published.Format(time.DateOnly), fn.Title)
}
func (fn *FakeNewsDto) Id() string {
	str := fmt.Sprintf("%v:%v:%v", fn.SiteId, fn.Title, fn.Published.UnixMilli())
	bytes := []byte(str)
	hashedBytes := md5.Sum(bytes)
	hashStr := fmt.Sprintf("%x", hashedBytes)
	return hashStr
}

type SearchResult struct {
	HighlightedWords []string          `json:"highlightedWords"`
	Items            []RssSearchResult `json:"items"`
}

type ChartDataset struct {
	Label string `json:"label"`
	Data  []int  `json:"data"`
}

type ChartResult struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Labels   []string       `json:"labels"`
	Datasets []ChartDataset `json:"datasets"`
}

type ChartsResult struct {
	Charts []ChartResult `json:"charts"`
}

func MakeLineChartFromSearchQueryCount(searchQueryCounts []SearchQueryCount, title string, datasetLabel string) ChartResult {
	dateFormat := "01-02"
	labels := make([]string, 7)
	data := make([]int, 7)
	weekItemsGroupedByDate := make(map[int64]int, 0)
	for _, v := range searchQueryCounts {
		weekItemsGroupedByDate[v.Timestamp.Unix()] = v.Count
	}

	today := time.Now()
	sevenDaysAgo := today.Add(-time.Hour * 24 * 6)
	i := 0
	for d := sevenDaysAgo; !d.After(today); d = d.AddDate(0, 0, 1) {
		dKey := d.Truncate(time.Hour * 24)
		labels[i] = dKey.Format(dateFormat)
		datum, ok := weekItemsGroupedByDate[dKey.Unix()]
		if ok {
			data[i] = datum
		}
		i++
	}
	return ChartResult{
		Type:   "line",
		Title:  title,
		Labels: labels,
		Datasets: []ChartDataset{
			{
				Label: datasetLabel,
				Data:  data,
			},
		},
	}
}

func MakeDoughnutChartFromSiteCount(siteCounts []SiteCount, title string) ChartResult {

	labels := make([]string, len(siteCounts))
	data := make([]int, len(siteCounts))
	for i, siteCount := range siteCounts {
		labels[i] = siteCount.SiteName
		data[i] = siteCount.Count
	}

	return ChartResult{
		Type:   "pie",
		Title:  title,
		Labels: labels,
		Datasets: []ChartDataset{
			{
				Label: "",
				Data:  data,
			},
		},
	}
}
