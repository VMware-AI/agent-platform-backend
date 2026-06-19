package session

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a production session store backed by Redis (HLD §5.2).
type RedisStore struct {
	rdb        *redis.Client
	defaultTTL time.Duration
}

// NewRedisStore returns a Redis-backed Store.
func NewRedisStore(rdb *redis.Client, defaultTTL time.Duration) *RedisStore {
	return &RedisStore{rdb: rdb, defaultTTL: defaultTTL}
}

func (s *RedisStore) key(id string) string { return "session:" + id }

func (s *RedisStore) Create(d Data) (string, error) {
	id, err := NewID()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	ttl := time.Until(d.ExpiresAt)
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	if err := s.rdb.Set(context.Background(), s.key(id), b, ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *RedisStore) Get(id string) (Data, error) {
	b, err := s.rdb.Get(context.Background(), s.key(id)).Bytes()
	if err == redis.Nil {
		return Data{}, ErrNotFound
	}
	if err != nil {
		return Data{}, err
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		return Data{}, err
	}
	if time.Now().After(d.ExpiresAt) {
		_ = s.Delete(id)
		return Data{}, ErrNotFound
	}
	return d, nil
}

func (s *RedisStore) Delete(id string) error {
	return s.rdb.Del(context.Background(), s.key(id)).Err()
}
