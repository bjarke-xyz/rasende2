package core

import (
	"github.com/bjarke-xyz/rasende2/internal/config"
)

type AppContext struct {
	Config *config.Config
	Deps   *AppDeps
}

type AppDeps struct {
	Service  NewsService
	AiClient AiClient
}
