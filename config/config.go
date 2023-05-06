package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port      string
	DbConnStr string

	RedisConnStr  string
	RedisUsername string
	RedisPassword string
	RedisPrefix   string

	JobKey string

	AppEnv string
}

const (
	AppEnvDevelopment = "development"
	AppEnvProduction  = "production"
)

func (c *Config) ConnectionString() string {
	return c.DbConnStr
}

func (c *Config) RedisConnectionString() string {
	return c.RedisConnStr
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
		Port:          os.Getenv("PORT"),
		DbConnStr:     os.Getenv("DB_CONN_STR"),
		RedisConnStr:  os.Getenv("REDIS_CONN_STR"),
		RedisUsername: os.Getenv("REDIS_USERNAME"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisPrefix:   os.Getenv("REDIS_PREFIX"),
		JobKey:        os.Getenv("JOB_KEY"),
		AppEnv:        os.Getenv("APP_ENV"),
	}, nil
}
