package pkg

import (
	"github.com/bjarke-xyz/rasende2/config"
	"github.com/bjarke-xyz/rasende2/jobs"
)

type AppContext struct {
	Config     *config.Config
	JobManager jobs.JobManager
	Cache      *CacheService
}
