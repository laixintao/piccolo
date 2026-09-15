package sd

import (
	"container/list"
	"time"
)

const (
	advertiseCacheTTL = 5 * time.Hour
	// DefaultAdvertiseCacheMaxKeys bounds the per-Pi advertisement cache.
	DefaultAdvertiseCacheMaxKeys = 100000
)

type advertisedKey struct {
	key     string
	expires time.Time
}

// advertisementCache is an LRU cache with a fixed TTL after each successful
// write. The caller holds keyUpdates while accessing it.
type advertisementCache struct {
	maxKeys     int
	keys        map[string]*list.Element
	order       list.List // most recently used first
	nextCleanup time.Time
}

func newAdvertisementCache(maxKeys int) *advertisementCache {
	return &advertisementCache{maxKeys: maxKeys, keys: make(map[string]*list.Element)}
}

func (c *advertisementCache) contains(key string, now time.Time) bool {
	element, ok := c.keys[key]
	if !ok {
		return false
	}
	if !now.Before(element.Value.(advertisedKey).expires) {
		c.remove(element)
		return false
	}
	c.order.MoveToFront(element)
	return true
}

func (c *advertisementCache) record(key string, expires time.Time) (evicted bool) {
	entry := advertisedKey{key: key, expires: expires}
	if element, ok := c.keys[key]; ok {
		element.Value = entry
		c.order.MoveToFront(element)
		return false
	}
	if len(c.keys) == c.maxKeys {
		c.remove(c.order.Back())
		evicted = true
	}
	c.keys[key] = c.order.PushFront(entry)
	return evicted
}

func (c *advertisementCache) remove(element *list.Element) {
	delete(c.keys, element.Value.(advertisedKey).key)
	c.order.Remove(element)
}

func (c *advertisementCache) clear() {
	clear(c.keys)
	c.order.Init()
	c.nextCleanup = time.Time{}
}

// Reclaim expired entries lazily without scanning the cache on every event.
// Individual lookups always check expiry, even between cleanups.
func (c *advertisementCache) prune(now time.Time) {
	if now.Before(c.nextCleanup) {
		return
	}
	for _, element := range c.keys {
		if !now.Before(element.Value.(advertisedKey).expires) {
			c.remove(element)
		}
	}
	c.nextCleanup = now.Add(advertiseCacheTTL)
}

func (c *advertisementCache) replace(keys []string, now time.Time) {
	c.clear()
	expires := now.Add(advertiseCacheTTL)
	for _, key := range keys {
		if len(c.keys) == c.maxKeys {
			break
		}
		if key != "" {
			c.record(key, expires)
		}
	}
	c.nextCleanup = expires
}
