package duda

import (
	"log"
	"strings"
)

func PrintPotentialRssFeedSites(cache *Cache) {
	dudaScraper := NewScraper(cache)
	links, err := dudaScraper.GetMediaUrls()
	if err != nil {
		log.Fatal(err)
	}
	workingLinks, err := dudaScraper.DownloadContents(links)
	if err != nil {
		log.Fatal(err)
	}

	for i, link := range workingLinks {
		content, err := dudaScraper.GetContent(link)
		if err != nil {
			log.Printf("error getting content for %v: %v", link.Url, err)
		}
		if strings.Contains(content, "rss") {
			log.Printf("(%v) %v: %v", i, link.Url, len(content))
		}
	}
}
