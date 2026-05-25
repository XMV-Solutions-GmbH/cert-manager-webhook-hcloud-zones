// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package solver

import (
	"sync"
	"time"
)

// zoneCacheKey scopes a cached zone-ID by the (SecretRef-string, zone)
// pair. Two credentials may legitimately reference different zone IDs for
// the same zone-apex name if they point at different Hetzner projects;
// scoping by the SecretRef-string keeps those entries distinct.
type zoneCacheKey struct {
	secretRef string
	zoneName  string
}

type zoneCacheEntry struct {
	id        int64
	expiresAt time.Time
}

// zoneCache is a tiny TTL-bounded map of (credential, zone-name) → zone-ID.
// Per docs/app-concept.md § 6.5 the default TTL is 30s; the cache exists
// purely to spare the per-challenge ListZones round-trip in steady-state.
type zoneCache struct {
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	store map[zoneCacheKey]zoneCacheEntry
}

func newZoneCache(ttl time.Duration, now func() time.Time) *zoneCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if now == nil {
		now = time.Now
	}
	return &zoneCache{
		ttl:   ttl,
		now:   now,
		store: make(map[zoneCacheKey]zoneCacheEntry),
	}
}

// Lookup returns (id, true) if the cache has a non-expired entry for the
// (secretRef, zone) pair, (0, false) otherwise.
func (c *zoneCache) Lookup(secretRef, zone string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.store[zoneCacheKey{secretRef: secretRef, zoneName: zone}]
	if !ok {
		return 0, false
	}
	if c.now().After(entry.expiresAt) {
		delete(c.store, zoneCacheKey{secretRef: secretRef, zoneName: zone})
		return 0, false
	}
	return entry.id, true
}

// Store inserts (or refreshes) the cached zone-ID for the pair.
func (c *zoneCache) Store(secretRef, zone string, id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[zoneCacheKey{secretRef: secretRef, zoneName: zone}] = zoneCacheEntry{
		id:        id,
		expiresAt: c.now().Add(c.ttl),
	}
}

// Invalidate drops the cached entry for the pair, if any. Used after a
// 404 response (zone deleted or moved between projects) so the next call
// re-queries.
func (c *zoneCache) Invalidate(secretRef, zone string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, zoneCacheKey{secretRef: secretRef, zoneName: zone})
}
