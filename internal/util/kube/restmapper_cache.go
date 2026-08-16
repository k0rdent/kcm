// Copyright 2026
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// restMapperCache holds one RESTMapper per remote cluster identity. A
// client.New that leaves Options.Mapper unset gets a dynamic mapper of its
// own, and each fresh mapper discovers the target apiserver's API surface on
// its first RESTMapping — two requests, one of which carries every group on a
// server supporting aggregated discovery.
//
// Keyed by apiserver URL plus a canonical fingerprint of the kubeconfig: a
// mapper owns a discovery client with its credentials baked in, and several
// identities may legitimately address one apiserver (distinct service
// accounts, impersonation), so a single slot per host would have them evict
// each other on every alternation. A rotated credential therefore adds an
// entry, and the superseded one is reclaimed by the idle TTL once nothing
// looks it up anymore.
//
// aliases indexes each entry's current byte representation, so steady-state
// lookups hash the bytes and skip parsing; an equivalent but byte-different
// kubeconfig (e.g. a reordered Secret rewrite) is canonicalized once, promoted
// into the index, and rides the fast path thereafter. Promotion swaps the
// entry's single alias rather than accumulating every representation seen, so
// len(aliases) == len(entries) always and the index cannot outgrow the cache.
type restMapperCache struct {
	entries map[string]*restMapperEntry
	// aliases maps an entry's current raw kubeconfig fingerprint to its
	// entries key.
	aliases map[string]string
	nowFunc func() time.Time
	// canonicalize is canonicalKubeconfigFingerprint, injectable so tests can
	// count how often lookups leave the fast path.
	canonicalize func([]byte) (string, error)

	ttl             time.Duration
	refreshInterval time.Duration
	sweepInterval   time.Duration

	lastSweep atomic.Int64
	mu        sync.RWMutex
}

func newRESTMapperCache(ttl, refreshInterval, sweepInterval time.Duration) *restMapperCache {
	return newRESTMapperCacheWithClock(ttl, refreshInterval, sweepInterval, time.Now)
}

func newRESTMapperCacheWithClock(ttl, refreshInterval, sweepInterval time.Duration, nowFunc func() time.Time) *restMapperCache {
	return &restMapperCache{
		entries:         make(map[string]*restMapperEntry),
		aliases:         make(map[string]string),
		nowFunc:         nowFunc,
		canonicalize:    canonicalKubeconfigFingerprint,
		ttl:             ttl,
		refreshInterval: refreshInterval,
		sweepInterval:   sweepInterval,
	}
}

type restMapperEntry struct {
	mapper meta.RESTMapper
	// createdAt is set once at store time and never refreshed by hits, unlike
	// lastUsed, so an entry's absolute age keeps growing while it is in use.
	createdAt time.Time
	// rawFingerprint is the entry's current byte representation — the one the
	// aliases index resolves. Mutated only under the cache's write lock, when a
	// promotion swaps it for a newer representation.
	rawFingerprint string
	lastUsed       atomic.Int64
}

// aged reports whether the entry has passed the absolute rebuild deadline. An
// aged entry is treated as a miss even if recently used; lastUsed drives idle
// eviction only.
func (e *restMapperEntry) aged(now time.Time, refreshInterval time.Duration) bool {
	return now.Sub(e.createdAt) >= refreshInterval
}

const (
	// restMapperTTL is how long an unused mapper is kept, so that deleted
	// clusters do not retain a discovery cache for the life of the process.
	restMapperTTL = time.Hour

	// restMapperRefreshInterval bounds a mapper's absolute age. The dynamic
	// mapper re-discovers only on a NoMatch and serves a mapping it already
	// knows from memory forever, so a cluster looked up more often than the TTL
	// would otherwise keep a removed API version or a changed CRD scope alive
	// until the process restarts. An aged entry is rebuilt on its next lookup.
	restMapperRefreshInterval = 30 * time.Minute

	// restMapperSweepInterval is how often eviction runs. It is driven by elapsed
	// time rather than by cache misses: deleting a cluster produces no miss, so a
	// miss-driven sweep would never reclaim a fleet that only shrinks.
	restMapperSweepInterval = 10 * time.Minute
)

var sharedRESTMapperCache = newRESTMapperCache(restMapperTTL, restMapperRefreshInterval, restMapperSweepInterval)

func normalizeHost(host string) string { return strings.TrimRight(host, "/") }

func (c *restMapperCache) get(cfg *rest.Config, kubeconfig []byte) (meta.RESTMapper, error) {
	now := c.nowFunc()
	c.maybeSweep(now)
	rawFingerprint := fingerprint(kubeconfig)

	c.mu.RLock()
	if key, ok := c.aliases[rawFingerprint]; ok {
		if entry, ok := c.entries[key]; ok && !entry.aged(now, c.refreshInterval) {
			entry.lastUsed.Store(now.UnixNano())
			c.mu.RUnlock()
			return entry.mapper, nil
		}
	}
	c.mu.RUnlock()

	host := normalizeHost(cfg.Host)
	canonicalFingerprint, err := c.canonicalize(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to fingerprint kubeconfig for %s: %w", host, err)
	}
	key := host + "\x00" + canonicalFingerprint

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && !entry.aged(now, c.refreshInterval) {
		entry.lastUsed.Store(now.UnixNano())
		c.promote(entry, key, rawFingerprint)
		c.mu.Unlock()
		return entry.mapper, nil
	}
	c.mu.Unlock()

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for %s: %w", host, err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST mapper for %s: %w", host, err)
	}

	// Re-check under the write lock: callers must converge on one mapper per
	// cluster, or several would each run their own discovery. If the entry moved
	// on while this build was in flight, a store would clobber newer credentials
	// with older ones, so the loser keeps its own mapper and caches nothing.
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && !entry.aged(now, c.refreshInterval) {
		entry.lastUsed.Store(now.UnixNano())
		c.promote(entry, key, rawFingerprint)
		return entry.mapper, nil
	}

	// An aged predecessor keeps its slot's key but not its alias: the new entry
	// resolves from the bytes that built it.
	if old, ok := c.entries[key]; ok {
		delete(c.aliases, old.rawFingerprint)
	}
	e := &restMapperEntry{mapper: mapper, rawFingerprint: rawFingerprint, createdAt: now}
	e.lastUsed.Store(now.UnixNano())
	c.entries[key] = e
	c.aliases[rawFingerprint] = key

	return mapper, nil
}

// promote makes rawFingerprint the entry's current byte representation, so its
// later lookups take the fast path instead of canonicalizing again. The
// previous representation's alias is dropped rather than kept: retaining every
// representation ever seen would grow the index without bound on repeated
// Secret rewrites. Must be called under the write lock.
func (c *restMapperCache) promote(entry *restMapperEntry, key, rawFingerprint string) {
	if entry.rawFingerprint == rawFingerprint {
		return
	}
	delete(c.aliases, entry.rawFingerprint)
	entry.rawFingerprint = rawFingerprint
	c.aliases[rawFingerprint] = key
}

func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalKubeconfigFingerprint(kubeconfig []byte) (string, error) {
	config, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return "", err
	}
	for _, cluster := range config.Clusters {
		cluster.Server = normalizeHost(cluster.Server)
	}
	canonical, err := clientcmd.Write(*config)
	if err != nil {
		return "", err
	}
	return fingerprint(canonical), nil
}

func (c *restMapperCache) maybeSweep(now time.Time) {
	last := c.lastSweep.Load()
	if now.UnixNano()-last < int64(c.sweepInterval) {
		return
	}
	// One sweeper at a time; a loser simply skips this round.
	if !c.lastSweep.CompareAndSwap(last, now.UnixNano()) {
		return
	}

	c.evictStale(now)
}

func (c *restMapperCache) evictStale(now time.Time) {
	cutoff := now.Add(-c.ttl).UnixNano()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		if e.lastUsed.Load() < cutoff {
			delete(c.entries, key)
			delete(c.aliases, e.rawFingerprint)
		}
	}
}

func (c *restMapperCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
