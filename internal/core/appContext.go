package core

import (
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/mail"
)

type AppContext struct {
	Config *config.Config
	Infra  *AppInfra
	Deps   *AppDeps
}

type AppInfra struct {
	Mail *mail.MailService
}

type AppDeps struct {
	Service  NewsService
	AiClient AiClient
}
