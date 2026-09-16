package handler

import (
	"container/list"
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"golang.org/x/sync/singleflight"
)

const (
	DefaultPeerCacheMaxKeys         = 1024
	DefaultPeerCacheMaxHolders      = 100000
	DefaultPeerCacheRefreshInterval = 10 * time.Second
	peerQueryTimeout                = 2 * time.Second
)

type PeerCacheConfig struct {
	MaxKeys         int
	MaxHolders      int
	RefreshInterval time.Duration
}

func DefaultPeerCacheConfig() PeerCacheConfig {
	return PeerCacheConfig{DefaultPeerCacheMaxKeys, DefaultPeerCacheMaxHolders, DefaultPeerCacheRefreshInterval}
}

func (c PeerCacheConfig) Validate() error {
	if c.MaxKeys <= 0 {
		return fmt.Errorf("--peer-cache-max-keys must be positive")
	}
	if c.MaxHolders <= 0 {
		return fmt.Errorf("--peer-cache-max-holders must be positive")
	}
	if c.RefreshInterval <= 0 {
		return fmt.Errorf("--peer-cache-refresh-interval must be positive")
	}
	return nil
}

type peerCacheKey struct{ group, key string }

type peerWindow struct {
	key       peerCacheKey
	holders   []string // Immutable after publication; selection works on a copy.
	after     string
	refreshAt time.Time
}

type holderWindowReader func(context.Context, string, string, string, int) ([]string, error)

type peerCache struct {
	mu          sync.Mutex // Protects entries, order, and holderCount; never held during IO.
	entries     map[peerCacheKey]*list.Element
	order       list.List
	holderCount int
	refreshes   singleflight.Group
	config      PeerCacheConfig
	read        holderWindowReader
	now         func() time.Time
}

func newPeerCache(config PeerCacheConfig, read holderWindowReader) (*peerCache, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &peerCache{
		entries: make(map[peerCacheKey]*list.Element), config: config, read: read, now: time.Now,
	}, nil
}

// get returns a private copy so callers can filter and shuffle concurrently.
// Refreshes are coalesced per (group, key). A canceled caller stops waiting while
// the shared, time-bounded query can still serve the other callers.
func (c *peerCache) get(ctx context.Context, group, key string) ([]string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	cacheKey := peerCacheKey{group, key}
	if window, fresh := c.lookup(cacheKey); fresh {
		return append([]string(nil), window.holders...), "hit", nil
	}
	// Length-prefix the group so arbitrary group/key strings cannot collide.
	flightKey := strconv.Itoa(len(group)) + ":" + group + key
	result := c.refreshes.DoChan(flightKey, func() (any, error) {
		window, fresh := c.lookup(cacheKey)
		if fresh {
			return window, nil
		}
		queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), peerQueryTimeout)
		defer cancel()
		limit := min(storage.FindKeyMaxResults, c.config.MaxHolders)
		holders, err := c.read(queryCtx, group, key, window.after, limit)
		if err != nil {
			// Keep the expired cursor so a retry does not restart at the first page.
			return nil, err
		}
		window = peerWindow{key: cacheKey, holders: holders}
		if len(holders) == limit {
			window.after = holders[len(holders)-1]
		}
		// Refresh on demand after 80-100% of the interval. Hits do not extend it.
		jitter := time.Duration(rand.Int64N(int64(c.config.RefreshInterval/5) + 1))
		window.refreshAt = c.now().Add(c.config.RefreshInterval - jitter)
		c.store(window)
		return window, nil
	})
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, "", result.Err
		}
		source := "refresh"
		if result.Shared {
			source = "shared_refresh"
		}
		window := result.Val.(peerWindow)
		return append([]string(nil), window.holders...), source, nil
	}
}

func (c *peerCache) lookup(key peerCacheKey) (peerWindow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return peerWindow{}, false
	}
	c.order.MoveToFront(element)
	window := element.Value.(peerWindow)
	return window, c.now().Before(window.refreshAt)
}

func (c *peerCache) store(window peerWindow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, ok := c.entries[window.key]; ok {
		c.remove(previous)
	}
	// Do not cache misses: a newly advertised key must be discoverable immediately.
	if len(window.holders) == 0 {
		return
	}
	for len(c.entries) >= c.config.MaxKeys || c.holderCount+len(window.holders) > c.config.MaxHolders {
		c.remove(c.order.Back())
	}
	c.entries[window.key] = c.order.PushFront(window)
	c.holderCount += len(window.holders)
}

// remove requires mu to be held.
func (c *peerCache) remove(element *list.Element) {
	window := element.Value.(peerWindow)
	delete(c.entries, window.key)
	c.holderCount -= len(window.holders)
	c.order.Remove(element)
}
