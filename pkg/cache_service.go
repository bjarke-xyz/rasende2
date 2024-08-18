package pkg

import (
	"encoding/json"
	"log"
)

type CacheService struct {
	cacheRepo *CacheRepo
}

func NewCacheService(cacheRepo *CacheRepo) *CacheService {
	return &CacheService{
		cacheRepo: cacheRepo,
	}
}

func (c *CacheService) Insert(key string, value string, expirationMinutes int) error {
	err := c.cacheRepo.Insert(key, value, expirationMinutes)
	if err != nil {
		log.Printf("error inserting to cache: %v", err)
	}
	return err
}

func (c *CacheService) InsertObj(key string, value any, expirationMinutes int) error {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	jsonStr := string(jsonBytes)
	return c.Insert(key, jsonStr, expirationMinutes)
}

func (c *CacheService) Get(key string) (string, error) {
	value, err := c.cacheRepo.Get(key)
	if err != nil {
		log.Printf("error getting from cache: %v", err)
	}
	return value, err
}

func (c *CacheService) GetObj(key string, target any) (bool, error) {
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

func (c *CacheService) DeleteExpired() error {
	err := c.cacheRepo.DeleteExpired()
	if err != nil {
		log.Printf("error deleting expired: %v", err)
	}
	return nil
}
