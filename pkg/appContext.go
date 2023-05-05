package pkg

import (
	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/bjarke-xyz/rasende2-api/db"
	"github.com/bjarke-xyz/rasende2-api/jobs"
)

type AppContext struct {
	Cache      *db.RedisCache
	Config     *config.Config
	JobManager jobs.JobManager
}
