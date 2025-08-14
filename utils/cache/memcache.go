package cache

import (
	"time"

	"github.com/dgraph-io/ristretto"
)

type MemCache struct {
	cache *ristretto.Cache
}

func (m *MemCache) SetWithTTL(key string, value interface{}, cost int64, ttl time.Duration) bool {
	if ok := m.cache.SetWithTTL(key, value, cost, ttl); !ok {
		return ok
	}
	m.cache.Wait()
	return true
}

func (m *MemCache) Get(key string) (interface{}, bool) {
	return m.cache.Get(key)
}

func (m *MemCache) Delete(key string) {
	m.cache.Del(key)
}

func NewMemCache() (*MemCache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e5,             // number of keys to track frequency of.
		MaxCost:     200 * (1 << 20), // maximum cost of cache (100MB).
		BufferItems: 64,              // number of keys per Get buffer.
	})

	if err != nil {
		return nil, err
	}

	return &MemCache{
		cache: cache,
	}, nil
}
