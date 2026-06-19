package session

import (
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisStore(t *testing.T) (*RedisStore, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisStore(rdb, time.Hour), func() { _ = rdb.Close(); mr.Close() }
}

func TestRedisStore_CreateGetDelete(t *testing.T) {
	s, cleanup := newRedisStore(t)
	defer cleanup()

	id, err := s.Create(Data{UserID: "u1", Role: "admin", MustChange: true, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u1" || got.Role != "admin" || !got.MustChange {
		t.Fatalf("Get = %+v", got)
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete want ErrNotFound, got %v", err)
	}
}

func TestRedisStore_MissingIsNotFound(t *testing.T) {
	s, cleanup := newRedisStore(t)
	defer cleanup()
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
