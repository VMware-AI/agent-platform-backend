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

func (s *RedisStore) key(id string) string      { return "session:" + id }
func (s *RedisStore) userKey(uid string) string { return "user_sessions:" + uid }

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
	ctx := context.Background()
	if err := s.rdb.Set(ctx, s.key(id), b, ttl).Err(); err != nil {
		return "", err
	}
	// Index the session under its user for DeleteByUser. Give the index set a
	// GENEROUS ttl (well beyond any single session's) so it can never expire while a
	// live session still points into it — otherwise DeleteByUser (reset/disable/
	// delete) would SMEMBERS an empty set and silently fail to revoke. Dead ids that
	// linger are harmless (Del no-ops on them).
	if d.UserID != "" {
		indexTTL := ttl + s.defaultTTL
		s.rdb.SAdd(ctx, s.userKey(d.UserID), id)
		s.rdb.Expire(ctx, s.userKey(d.UserID), indexTTL)
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

// DeleteByUser revokes every session indexed under the user (all devices).
func (s *RedisStore) DeleteByUser(userID string) error {
	ctx := context.Background()
	ids, err := s.rdb.SMembers(ctx, s.userKey(userID)).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	keys := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		keys = append(keys, s.key(id))
	}
	keys = append(keys, s.userKey(userID))
	return s.rdb.Del(ctx, keys...).Err()
}
