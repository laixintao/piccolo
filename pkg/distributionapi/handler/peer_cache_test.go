package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"github.com/stretchr/testify/require"
)

func testPeerCache(t *testing.T, config PeerCacheConfig, read holderWindowReader) (*peerCache, *atomic.Int64) {
	t.Helper()
	cache, err := newPeerCache(config, read)
	require.NoError(t, err)
	now := new(atomic.Int64)
	now.Store(time.Now().UnixNano())
	cache.now = func() time.Time { return time.Unix(0, now.Load()) }
	return cache, now
}

func TestPeerCacheRotatesAcrossOneHundredThousandHolders(t *testing.T) {
	holders := make([]string, 100000)
	for i := range holders {
		holders[i] = fmt.Sprintf("10.%d.%d.%d:5127", i>>16, (i>>8)&255, i&255)
	}
	sort.Strings(holders)
	var calls atomic.Int32
	cache, now := testPeerCache(t, DefaultPeerCacheConfig(), func(_ context.Context, group, key, after string, limit int) ([]string, error) {
		calls.Add(1)
		start := sort.Search(len(holders), func(i int) bool { return holders[i] > after })
		window := make([]string, min(limit, len(holders)))
		for i := range window {
			window[i] = holders[(start+i)%len(holders)]
		}
		return window, nil
	})
	seen := make(map[string]bool)
	for i := 0; i < len(holders)/storage.FindKeyMaxResults; i++ {
		window, _, err := cache.get(context.Background(), "group", "hot-blob")
		require.NoError(t, err)
		require.Len(t, window, storage.FindKeyMaxResults)
		for _, holder := range window {
			require.False(t, seen[holder], "repeated before completing the scan: %s", holder)
			seen[holder] = true
		}
		now.Add(int64(DefaultPeerCacheRefreshInterval))
	}
	require.Len(t, seen, len(holders))
	require.EqualValues(t, 50, calls.Load())
	window, _, err := cache.get(context.Background(), "group", "hot-blob")
	require.NoError(t, err)
	require.Equal(t, holders[:storage.FindKeyMaxResults], window)
	require.Equal(t, storage.FindKeyMaxResults, cache.holderCount)
}

func TestPeerCacheHitsDoNotExtendRefreshAndReturnPrivateCopies(t *testing.T) {
	var calls atomic.Int32
	cache, now := testPeerCache(t, DefaultPeerCacheConfig(), func(context.Context, string, string, string, int) ([]string, error) {
		calls.Add(1)
		return []string{"a", "b", "c"}, nil
	})
	window, _, err := cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	window[0] = "changed by caller"
	now.Add(int64(7 * time.Second)) // Before the earliest jittered expiry (8s).
	window, source, err := cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	require.Equal(t, "hit", source)
	require.Equal(t, []string{"a", "b", "c"}, window)
	require.EqualValues(t, 1, calls.Load())
	now.Add(int64(3 * time.Second))
	_, _, err = cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
}

func TestPeerCacheEnforcesKeyAndTotalHolderBudgets(t *testing.T) {
	for _, tt := range []struct {
		name                               string
		maxKeys, maxHolders, holdersPerKey int
	}{
		{"key budget", 2, 100, 1},
		{"holder budget", 100, 6, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := PeerCacheConfig{tt.maxKeys, tt.maxHolders, time.Minute}
			cache, _ := testPeerCache(t, config, func(_ context.Context, group, key, after string, limit int) ([]string, error) {
				holders := make([]string, tt.holdersPerKey)
				for i := range holders {
					holders[i] = fmt.Sprintf("%s-%s-%d", group, key, i)
				}
				return holders, nil
			})
			for _, group := range []string{"a", "b", "a", "c"} {
				_, _, err := cache.get(context.Background(), group, "same-digest")
				require.NoError(t, err)
				require.LessOrEqual(t, len(cache.entries), config.MaxKeys)
				require.LessOrEqual(t, cache.holderCount, config.MaxHolders)
			}
			// The group is part of the key; touching a keeps it over b.
			require.Contains(t, cache.entries, peerCacheKey{"a", "same-digest"})
			require.NotContains(t, cache.entries, peerCacheKey{"b", "same-digest"})
			require.Contains(t, cache.entries, peerCacheKey{"c", "same-digest"})
		})
	}
}

func TestPeerCacheInvalidateRemovesOnlyTouchedKeys(t *testing.T) {
	cache, _ := testPeerCache(t, DefaultPeerCacheConfig(), func(context.Context, string, string, string, int) ([]string, error) {
		return nil, nil
	})
	cache.store(peerWindow{
		key:       peerCacheKey{"group", "stale"},
		holders:   []string{"a", "b"},
		refreshAt: time.Now().Add(time.Minute),
	})
	cache.store(peerWindow{
		key:       peerCacheKey{"group", "fresh"},
		holders:   []string{"c"},
		refreshAt: time.Now().Add(time.Minute),
	})
	cache.store(peerWindow{
		key:       peerCacheKey{"other", "stale"},
		holders:   []string{"d"},
		refreshAt: time.Now().Add(time.Minute),
	})

	cache.invalidate("group", "stale", "missing", "")

	require.NotContains(t, cache.entries, peerCacheKey{"group", "stale"})
	require.Contains(t, cache.entries, peerCacheKey{"group", "fresh"})
	require.Contains(t, cache.entries, peerCacheKey{"other", "stale"})
	require.Equal(t, 2, cache.holderCount)
}

func TestPeerCacheErrorsKeepCursorAndMissesAreNotCached(t *testing.T) {
	var calls int
	var cursors []string
	queryErr := errors.New("database unavailable")
	cache, now := testPeerCache(t, PeerCacheConfig{2, 2, time.Second}, func(_ context.Context, group, key, after string, limit int) ([]string, error) {
		calls++
		cursors = append(cursors, after)
		switch calls {
		case 1:
			return []string{"a", "b"}, nil
		case 2:
			return nil, queryErr
		case 3:
			return nil, nil // The key was withdrawn.
		default:
			return []string{"new-holder"}, nil
		}
	})
	_, _, err := cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	now.Add(int64(time.Second))
	_, _, err = cache.get(context.Background(), "g", "k")
	require.ErrorIs(t, err, queryErr)
	window, _, err := cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	require.Empty(t, window)
	require.Empty(t, cache.entries)
	require.Zero(t, cache.holderCount)
	window, _, err = cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	require.Equal(t, []string{"new-holder"}, window)
	require.Equal(t, []string{"", "b", "b", ""}, cursors)
}

func TestPeerCacheCoalescesRefreshAndAllowsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	cache, _ := testPeerCache(t, DefaultPeerCacheConfig(), func(ctx context.Context, group, key, after string, limit int) ([]string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return []string{"a", "b"}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, _, err := cache.get(ctx, "g", "k"); first <- err }()
	<-started
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)

	const waiters = 20
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			window, _, err := cache.get(context.Background(), "g", "k")
			if err == nil && len(window) != 2 {
				err = fmt.Errorf("unexpected window: %v", window)
			}
			if len(window) > 0 {
				window[0] = "private copy"
			}
			results <- err
		}()
	}
	close(release)
	for i := 0; i < waiters; i++ {
		require.NoError(t, <-results)
	}
	require.EqualValues(t, 1, calls.Load())
	window, _, err := cache.get(context.Background(), "g", "k")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, window)
}

func TestPeerCacheDifferentKeysRefreshIndependently(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cache, _ := testPeerCache(t, DefaultPeerCacheConfig(), func(ctx context.Context, group, key, after string, limit int) ([]string, error) {
		if key == "slow" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []string{key}, nil
	})
	done := make(chan error, 1)
	go func() { _, _, err := cache.get(context.Background(), "g", "slow"); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	window, _, err := cache.get(ctx, "g", "fast")
	close(release)
	require.NoError(t, err)
	require.Equal(t, []string{"fast"}, window)
	require.NoError(t, <-done)
}
