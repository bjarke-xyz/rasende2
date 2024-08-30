package pkg

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bjarke-xyz/rasende2/config"
	"github.com/bjarke-xyz/rasende2/db"
)

type memoryCacheItem struct {
	key       string
	value     string
	expiresAt int64
}

type CacheRepo struct {
	cfg         *config.Config
	memoryFirst bool
	inmem       sync.Map
}

func NewCacheRepo(cfg *config.Config, memoryFirst bool) *CacheRepo {
	return &CacheRepo{
		cfg:         cfg,
		memoryFirst: memoryFirst,
	}
}

func (c *CacheRepo) Insert(key string, value string, expirationMinutes int) error {
	db, err := db.Open(c.cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(expirationMinutes) * time.Minute).Unix()
	if c.memoryFirst {
		memitem := memoryCacheItem{
			key:       key,
			value:     value,
			expiresAt: expiresAt,
		}
		c.inmem.Store(key, memitem)
	}
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
	if c.memoryFirst {
		item, ok := c.inmem.Load(key)
		if ok {
			memitem := item.(memoryCacheItem)
			if memitem.expiresAt > now {
				return memitem.value, nil
			}
		}
	}
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
	if c.memoryFirst {
		c.inmem.Range(func(key, value any) bool {
			memitem := value.(memoryCacheItem)
			if memitem.expiresAt < now {
				c.inmem.Delete(key)
			}
			return true
		})
	}
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
	if c.memoryFirst {
		c.inmem.Range(func(key, value any) bool {
			strKey := key.(string)
			if strings.HasPrefix(strKey, prefix) {
				c.inmem.Delete(key)
			}
			return true
		})
	}
	_, err = db.Exec("DELETE FROM cache WHERE k LIKE ?", prefix+"%")
	if err != nil {
		return fmt.Errorf("error when deleting from cache by prefix %v: %w", prefix, err)
	}
	return nil
}
