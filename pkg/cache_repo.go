package pkg

import (
	"fmt"
	"time"

	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/bjarke-xyz/rasende2-api/db"
)

type CacheRepo struct {
	cfg *config.Config
}

func NewCacheRepo(cfg *config.Config) *CacheRepo {
	return &CacheRepo{
		cfg: cfg,
	}
}

func (c *CacheRepo) Insert(key string, value string, expirationMinutes int) error {
	db, err := db.Open(c.cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(expirationMinutes) * time.Minute).Unix()
	_, err = db.Exec("INSERT INTO cache (k, v, expires_at) VALUES (?, ?, ?) ON CONFLICT DO UPDATE SET v = excluded.v, expires_at = excluded.expires_at", key, value, expiresAt)
	if err != nil {
		return fmt.Errorf("error inserting key %v: %w", key, err)
	}
	return nil
}

func (c *CacheRepo) Get(key string) (string, error) {
	db, err := db.Open(c.cfg)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()
	value := []string{}
	err = db.Select(&value, "SELECT v FROM cache WHERE k = ? AND expires_at > ? LIMIT 1", key, now)
	if err != nil {
		return "", fmt.Errorf("error getting from cache, key=%v: %w", key, err)
	}
	if len(value) == 0 {
		return "", nil
	}
	return value[0], nil
}

func (c *CacheRepo) DeleteExpired() error {
	db, err := db.Open(c.cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	_, err = db.Exec("DELETE FROM cache WHERE expires_at < ?", now)
	if err != nil {
		return fmt.Errorf("error deleting from cache: %w", err)
	}
	return nil
}

func (c *CacheRepo) DeleteByPrefix(prefix string) error {
	db, err := db.Open(c.cfg)
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM cache WHERE k LIKE ?", prefix+"%")
	if err != nil {
		return fmt.Errorf("error when deleting from cache by prefix %v: %w", prefix, err)
	}
	return nil
}
