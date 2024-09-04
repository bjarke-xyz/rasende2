package main

import (
	"github.com/bjarke-xyz/rasende2/cmd/duda/duda"
)

func main() {
	cache := duda.NewCache("./cache")
	duda.PrintPotentialRssFeedSites(cache)
}
