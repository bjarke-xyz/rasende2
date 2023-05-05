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
	err := godotenv.Load()
	if err != nil {
		err = godotenv.Load("/run/secrets/env")
		if err != nil {
			return nil, fmt.Errorf("failed to load env: %w", err)
		}
	}
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
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisPrefix:   os.Getenv("REDIS_PREFIX"),
		JobKey:        os.Getenv("JOB_KEY"),
		AppEnv:        os.Getenv("APP_ENV"),
	}, nil
}
