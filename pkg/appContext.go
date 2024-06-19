package pkg

import (
	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/bjarke-xyz/rasende2-api/jobs"
)

type AppContext struct {
	Config     *config.Config
	JobManager jobs.JobManager
	Cache      *CacheService
}
