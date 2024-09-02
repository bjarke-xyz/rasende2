package repository

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
)

type memoryCacheItem struct {
	key       string
	value     string
	expiresAt int64
}

type cacheRepo struct {
	cfg         *config.Config
	memoryFirst bool
	inmem       sync.Map
}

func NewCacheRepo(cfg *config.Config, memoryFirst bool) *cacheRepo {
	return &cacheRepo{
		cfg:         cfg,
		memoryFirst: memoryFirst,
	}
}

func (c *cacheRepo) Insert(key string, value string, expirationMinutes int) error {
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

func (c *cacheRepo) Get(key string) (string, error) {
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

func (c *cacheRepo) DeleteExpired() error {
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

func (c *cacheRepo) DeleteByPrefix(prefix string) error {
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

type cacheService struct {
	cacheRepo *cacheRepo
}

func NewCacheService(cacheRepo *cacheRepo) core.Cache {
	return &cacheService{
		cacheRepo: cacheRepo,
	}
}

func (c *cacheService) Insert(key string, value string, expirationMinutes int) error {
	err := c.cacheRepo.Insert(key, value, expirationMinutes)
	if err != nil {
		log.Printf("error inserting to cache: %v", err)
	}
	return err
}

func (c *cacheService) InsertObj(key string, value any, expirationMinutes int) error {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	jsonStr := string(jsonBytes)
	return c.Insert(key, jsonStr, expirationMinutes)
}

func (c *cacheService) Get(key string) (string, error) {
	value, err := c.cacheRepo.Get(key)
	if err != nil {
		log.Printf("error getting from cache: %v", err)
	}
	return value, err
}

func (c *cacheService) GetObj(key string, target any) (bool, error) {
	value, err := c.Get(key)
	if err != nil {
		return false, err
	}
	if value == "" {
		return false, nil
	}
	valueBytes := []byte(value)
	err = json.Unmarshal(valueBytes, &target)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *cacheService) DeleteExpired() error {
	err := c.cacheRepo.DeleteExpired()
	if err != nil {
		log.Printf("error deleting expired: %v", err)
		return err
	}
	return nil
}

func (c *cacheService) DeleteByPrefix(prefix string) error {
	err := c.cacheRepo.DeleteByPrefix(prefix)
	if err != nil {
		log.Printf("error deleting by prefix %v: %v", prefix, err)
		return err
	}
	return nil
}
