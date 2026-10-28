package news

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordRSSFetchResultTracksFailureStreak(t *testing.T) {
	siteName := fmt.Sprintf("test-%d", time.Now().UnixNano())
	service := &RssService{}

	service.recordRSSFetchResult(siteName, errors.New("fetch failed"))
	failureStarted := rssFailureStarted.WithLabelValues(siteName)
	startedAt := testutil.ToFloat64(failureStarted)
	now := float64(time.Now().Unix())
	if startedAt < now-1 || startedAt > now {
		t.Fatalf("expected failure timestamp near now, got %v", startedAt)
	}

	service.recordRSSFetchResult(siteName, errors.New("fetch failed again"))
	if got := testutil.ToFloat64(failureStarted); got != startedAt {
		t.Fatalf("expected failure timestamp to remain unchanged during streak, got %v want %v", got, startedAt)
	}

	service.recordRSSFetchResult(siteName, nil)
	if got := testutil.ToFloat64(failureStarted); got != 0 {
		t.Fatalf("expected successful fetch to clear failure timestamp, got %v", got)
	}
}
