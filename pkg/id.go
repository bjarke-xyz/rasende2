package pkg

import "crypto/rand"

// NewID returns a URL-safe random identifier. Its alphabet must never contain
// '-': parseArticleSlugV2 splits an article slug on the first '-' to recover
// the id from it. rand.Text is base32, so that holds.
func NewID() string {
	return rand.Text()
}
