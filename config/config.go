package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port             string
	DbConnStr        string
	BackupDbPath     string
	TursoDatabaseUrl string
	TursoAuthToken   string

	S3BackupUrl             string
	S3BackupBucket          string
	S3BackupAccessKeyId     string
	S3BackupSecretAccessKey string

	S3ImagePublicBaseUrl   string
	S3ImageUrl             string
	S3ImageBucket          string
	S3ImageAccessKeyId     string
	S3ImageSecretAccessKey string

	SearchIndexPath string

	JobKey string

	OpenAIAPIKey string

	AppEnv string

	NtfyTopic string

	AdminPassword string

	BuildTime *time.Time

	UseFakeOpenAi bool
}

const (
	AppEnvDevelopment = "development"
	AppEnvProduction  = "production"
)

func (c *Config) ConnectionString() string {
	// return c.DbConnStr
	return fmt.Sprintf("%s?authToken=%s", c.TursoDatabaseUrl, c.TursoAuthToken)
}

func NewConfig() (*Config, error) {
	godotenv.Load()
	appEnv := os.Getenv("APP_ENV")
	if appEnv == "" {
		appEnv = AppEnvDevelopment
	} else {
		if appEnv != AppEnvDevelopment && appEnv != AppEnvProduction {
			return nil, fmt.Errorf("failed to validate APP_ENV: invalid value %q", appEnv)
		}
	}
	buildTimeStr := os.Getenv("BUILD_TIME")
	var buildTime *time.Time
	if buildTimeStr != "" {
		_buildTime, err := time.Parse("2006-01-02 15:04:05", buildTimeStr)
		if err != nil {
			log.Printf("error parsing BUILD_TIME env: %v", err)
		}
		buildTime = &_buildTime
	}
	return &Config{
		Port:                    os.Getenv("PORT"),
		DbConnStr:               os.Getenv("DB_CONN_STR"),
		BackupDbPath:            os.Getenv("BACKUP_DB_PATH"),
		TursoDatabaseUrl:        os.Getenv("TURSO_DATABASE_URL"),
		TursoAuthToken:          os.Getenv("TURSO_AUTH_TOKEN"),
		S3BackupUrl:             os.Getenv("S3_BACKUP_URL"),
		S3BackupBucket:          os.Getenv("S3_BACKUP_BUCKET"),
		S3BackupAccessKeyId:     os.Getenv("S3_BACKUP_ACCESS_KEY_ID"),
		S3BackupSecretAccessKey: os.Getenv("S3_BACKUP_SECRET_ACCESS_KEY"),
		S3ImagePublicBaseUrl:    os.Getenv("S3_IMAGE_PUBLIC_BASE_URL"),
		S3ImageUrl:              os.Getenv("S3_IMAGE_URL"),
		S3ImageBucket:           os.Getenv("S3_IMAGE_BUCKET"),
		S3ImageAccessKeyId:      os.Getenv("S3_IMAGE_ACCESS_KEY_ID"),
		S3ImageSecretAccessKey:  os.Getenv("S3_IMAGE_SECRET_ACCESS_KEY"),
		JobKey:                  os.Getenv("JOB_KEY"),
		OpenAIAPIKey:            os.Getenv("OPENAI_API_KEY"),
		AppEnv:                  os.Getenv("APP_ENV"),
		SearchIndexPath:         os.Getenv("SEARCH_INDEX_PATH"),
		NtfyTopic:               os.Getenv("NTFY_TOPIC_BACKUP"),
		AdminPassword:           os.Getenv("ADMIN_PASSWORD"),
		BuildTime:               buildTime,
		UseFakeOpenAi:           os.Getenv("USE_FAKE_OPENAI") == "true",
	}, nil
}
