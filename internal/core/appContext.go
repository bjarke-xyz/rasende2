package core

import (
	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/mail"
)

type AppContext struct {
	Config *config.Config
	Cache  Cache
	Mail   *mail.MailService
}
