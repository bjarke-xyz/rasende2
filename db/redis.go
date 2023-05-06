package db

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/bjarke-xyz/rasende2-api/config"
	"github.com/go-redis/cache/v8"
	"github.com/go-redis/redis/v8"
)

type RedisCache struct {
	cache     *cache.Cache
	keyPrefix string
}

func NewRedisCache(cfg *config.Config) (*RedisCache, error) {
	opt, err := redis.ParseURL(cfg.RedisConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis connection string: %w", err)
	}
	rdb := redis.NewClient(opt)
	mycache := cache.New(&cache.Options{
		Redis:      rdb,
		LocalCache: cache.NewTinyLFU(1000, time.Minute),
	})
	return &RedisCache{
		cache:     mycache,
		keyPrefix: cfg.RedisPrefix,
	}, nil
}

func (r *RedisCache) getKey(key string) string {
	return r.keyPrefix + ":" + key
}

func (r *RedisCache) Set(ctx context.Context, key string, value any, TTL time.Duration) error {
	err := r.cache.Set(&cache.Item{
		Ctx:   ctx,
		Key:   r.getKey(key),
		Value: value,
		TTL:   TTL,
	})
	if err != nil {
		log.Printf("cache set with key %v failed: %v", key, err)
	}
	return err
}

func (r *RedisCache) Get(ctx context.Context, key string, value any) error {
	err := r.cache.Get(ctx, r.getKey(key), value)
	if err != nil {
		if !errors.Is(err, cache.ErrCacheMiss) {
			log.Printf("cache get with key %v failed: %v", key, err)
		}
	}
	return err
}

func (r *RedisCache) Delete(ctx context.Context, key string) error {
	err := r.cache.Delete(ctx, r.getKey(key))
	if err != nil {
		if !errors.Is(err, cache.ErrCacheMiss) {
			log.Printf("cache delete with key %v failed: %v", key, err)
		}
	}
	return err
}
