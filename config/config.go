package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port         string
	DbConnStr    string
	BackupDbPath string

	S3BackupUrl             string
	S3BackupBucket          string
	S3BackupAccessKeyId     string
	S3BackupSecretAccessKey string

	SearchIndexPath string

	JobKey string

	OpenAIAPIKey string

	AppEnv string

	NtfyTopic string
}

const (
	AppEnvDevelopment = "development"
	AppEnvProduction  = "production"
)

func (c *Config) ConnectionString() string {
	return c.DbConnStr
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
	return &Config{
		Port:                    os.Getenv("PORT"),
		DbConnStr:               os.Getenv("DB_CONN_STR"),
		BackupDbPath:            os.Getenv("BACKUP_DB_PATH"),
		S3BackupUrl:             os.Getenv("S3_BACKUP_URL"),
		S3BackupBucket:          os.Getenv("S3_BACKUP_BUCKET"),
		S3BackupAccessKeyId:     os.Getenv("S3_BACKUP_ACCESS_KEY_ID"),
		S3BackupSecretAccessKey: os.Getenv("S3_BACKUP_SECRET_ACCESS_KEY"),
		JobKey:                  os.Getenv("JOB_KEY"),
		OpenAIAPIKey:            os.Getenv("OPENAI_API_KEY"),
		AppEnv:                  os.Getenv("APP_ENV"),
		SearchIndexPath:         os.Getenv("SEARCH_INDEX_PATH"),
		NtfyTopic:               os.Getenv("NTFY_TOPIC_BACKUP"),
	}, nil
}
