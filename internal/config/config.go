package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/joho/godotenv"
)

type Config struct {
	Port int
	// DbConnStr is the path to the local sqlite database file.
	DbConnStr string

	S3ImagePublicBaseUrl   string
	S3ImageUrl             string
	S3ImageBucket          string
	S3ImageAccessKeyId     string
	S3ImageSecretAccessKey string

	SmtpHost     string
	SmtpUsername string
	SmtpPassword string
	SmtpPort     string
	SmtpSender   string
	SmtpTest     bool

	JobKey string

	LLMAPIKey string

	AppEnv string

	AdminPassword string
	AdminEmail    string

	BuildTime *time.Time

	UseFakeLLM bool

	CookieSecret string

	BaseUrl string

	// OIDC configures the external auth server this app delegates login to.
	// The client id/secret are issued by that server's `auth project add`.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
}

// OIDCRedirectURI is the callback the auth server redirects back to after login.
// It must be registered as a redirect URI on the project there.
func (c *Config) OIDCRedirectURI() string {
	return c.BaseUrl + "/auth/callback"
}

const (
	AppEnvDevelopment = "development"
	AppEnvProduction  = "production"
)

// ConnectionString returns a modernc.org/sqlite DSN for the local database file.
//
// WAL lets the RSS fetcher write while requests read; busy_timeout absorbs the
// brief contention that remains, since SQLite still allows only one writer.
//
// _time_format=sqlite is required, not cosmetic: without it the driver binds
// time.Time using Go's default layout ("2006-01-02 15:04:05 -0700 MST"), which
// SQLite's date()/datetime() cannot parse. It writes "2006-01-02 15:04:05-07:00"
// instead, matching the timestamps already in the database.
func (c *Config) ConnectionString() string {
	return fmt.Sprintf("file:%s?_time_format=sqlite&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", c.DbConnStr)
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
		Port:                   pkg.MustAtoi(os.Getenv("PORT")),
		DbConnStr:              os.Getenv("DB_CONN_STR"),
		S3ImagePublicBaseUrl:   os.Getenv("S3_IMAGE_PUBLIC_BASE_URL"),
		S3ImageUrl:             os.Getenv("S3_IMAGE_URL"),
		S3ImageBucket:          os.Getenv("S3_IMAGE_BUCKET"),
		S3ImageAccessKeyId:     os.Getenv("S3_IMAGE_ACCESS_KEY_ID"),
		S3ImageSecretAccessKey: os.Getenv("S3_IMAGE_SECRET_ACCESS_KEY"),
		SmtpHost:               os.Getenv("SMTP_HOST"),
		SmtpUsername:           os.Getenv("SMTP_USERNAME"),
		SmtpPassword:           os.Getenv("SMTP_PASSWORD"),
		SmtpPort:               os.Getenv("SMTP_PORT"),
		SmtpSender:             os.Getenv("SMTP_SENDER"),
		SmtpTest:               os.Getenv("SMTP_TEST") == "true",
		JobKey:                 os.Getenv("JOB_KEY"),
		LLMAPIKey:              os.Getenv("LLM_API_KEY"),
		AppEnv:                 appEnv,
		AdminPassword:          os.Getenv("ADMIN_PASSWORD"),
		AdminEmail:             os.Getenv("ADMIN_EMAIL"),
		BuildTime:              buildTime,
		UseFakeLLM:             os.Getenv("USE_FAKE_LLM") == "true",
		CookieSecret:           os.Getenv("COOKIE_SECRET"),
		BaseUrl:                os.Getenv("BASE_URL"),
		OIDCIssuer:             os.Getenv("OIDC_ISSUER"),
		OIDCClientID:           os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:       os.Getenv("OIDC_CLIENT_SECRET"),
	}, nil
}
