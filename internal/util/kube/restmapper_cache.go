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
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
	entries map[restMapperKey]*restMapperEntry
	// aliases maps an entry's current raw kubeconfig fingerprint to its
	// entries key.
	aliases map[[sha256.Size]byte]restMapperKey
	nowFunc func() time.Time
	// canonicalize is canonicalKubeconfigFingerprint, injectable so tests can
	// count how often lookups leave the fast path.
	canonicalize func([]byte) ([sha256.Size]byte, error)

	ttl             time.Duration
	refreshInterval time.Duration
	sweepInterval   time.Duration
	maxEntries      int

	mu sync.Mutex
}

func newRESTMapperCache(ttl, refreshInterval, sweepInterval time.Duration, maxEntries int) *restMapperCache {
	return newRESTMapperCacheWithClock(ttl, refreshInterval, sweepInterval, maxEntries, time.Now)
}

// restMapperKey identifies one cluster identity: the normalized apiserver
// host plus the canonical fingerprint of the kubeconfig addressing it.
type restMapperKey struct {
	host        string
	fingerprint [sha256.Size]byte
}

func newRESTMapperCacheWithClock(ttl, refreshInterval, sweepInterval time.Duration, maxEntries int, nowFunc func() time.Time) *restMapperCache {
	return &restMapperCache{
		entries:         make(map[restMapperKey]*restMapperEntry),
		aliases:         make(map[[sha256.Size]byte]restMapperKey),
		nowFunc:         nowFunc,
		canonicalize:    canonicalKubeconfigFingerprint,
		ttl:             ttl,
		refreshInterval: refreshInterval,
		sweepInterval:   sweepInterval,
		maxEntries:      maxEntries,
	}
}

type restMapperEntry struct {
	mapper meta.RESTMapper
	// httpClient is the client the mapper discovers through. It is handed out
	// alongside the mapper so client.New reuses it instead of constructing a
	// second one per object client, and so a mapper is never paired with
	// another credential generation's transport.
	httpClient *http.Client
	// createdAt is set once at store time and never refreshed by hits, unlike
	// lastUsed, so an entry's absolute age keeps growing while it is in use.
	createdAt time.Time
	// rawFingerprint is the entry's current byte representation — the one the
	// aliases index resolves. Mutated only under the cache's lock, when a
	// promotion swaps it for a newer representation.
	rawFingerprint [sha256.Size]byte
	// lastUsed is read and written only while the cache's mu is held, like
	// every other mutable field here, so it needs no atomicity of its own.
	lastUsed int64
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

	// restMapperSweepInterval is the cadence of the sweeper runnable. TTL expiry
	// is driven by elapsed time rather than by lookups: deleting a cluster
	// produces no further lookups, so a lookup-driven sweep would never reclaim
	// a fleet that only shrinks.
	restMapperSweepInterval = 10 * time.Minute

	// restMapperMaxEntries caps the cache unless [restMapperCacheMaxEntriesEnvName]
	// overrides it. High identity churn within one TTL window must not grow the
	// map without bound, and the cap also bounds retention in a binary that
	// does not register the sweeper.
	restMapperMaxEntries = 256

	// restMapperMaxConfigurableEntries is the largest cap an override may set,
	// and must match the maximum of restMapperCacheMaxEntries in the chart
	// values schemas so both configuration paths agree. A larger value is
	// clamped rather than rejected: an operator asking for an oversized cache
	// wants it big, and falling back to the small default could reintroduce
	// the eviction churn the override exists to prevent.
	restMapperMaxConfigurableEntries = 1_000_000
)

// restMapperCacheMaxEntriesEnvName is the name of the env variable overriding
// the shared RESTMapper cache's maximum entry count.
const restMapperCacheMaxEntriesEnvName = "RESTMAPPER_CACHE_MAX_ENTRIES"

var sharedRESTMapperCache = newRESTMapperCache(restMapperTTL, restMapperRefreshInterval, restMapperSweepInterval, restMapperCacheMaxEntriesFromEnv())

// restMapperCacheMaxEntriesFromEnv resolves the shared cache's entry cap: the value of
// [restMapperCacheMaxEntriesEnvName] when it holds a positive integer,
// restMapperMaxEntries otherwise.
func restMapperCacheMaxEntriesFromEnv() int {
	raw, ok := os.LookupEnv(restMapperCacheMaxEntriesEnvName)
	if !ok {
		return restMapperMaxEntries
	}
	// On range overflow Atoi reports ErrRange but still returns the value
	// saturated to MaxInt or MinInt, so letting that error through makes an
	// oversized ask clamp below and a negative one fall back, the same as
	// their in-range counterparts.
	n, err := strconv.Atoi(raw)
	if (err != nil && !errors.Is(err, strconv.ErrRange)) || n <= 0 {
		return restMapperMaxEntries
	}
	return min(n, restMapperMaxConfigurableEntries)
}

func normalizeHost(host string) string { return strings.TrimRight(host, "/") }

// get returns the cached RESTMapper for the cluster identity the kubeconfig
// describes, together with the HTTP client the mapper was built on, so the
// caller can hand both to client.New and the object client shares the mapper's
// transport instead of constructing its own. The two always come from the same
// entry: a mapper must never be paired with another credential generation's
// transport.
func (c *restMapperCache) get(cfg *rest.Config, kubeconfig []byte) (meta.RESTMapper, *http.Client, error) {
	now := c.nowFunc()
	rawFingerprint := fingerprint(kubeconfig)

	c.mu.Lock()
	if key, ok := c.aliases[rawFingerprint]; ok {
		if entry, ok := c.entries[key]; ok && !entry.aged(now, c.refreshInterval) {
			entry.lastUsed = now.UnixNano()
			c.mu.Unlock()
			return entry.mapper, entry.httpClient, nil
		}
	}
	c.mu.Unlock()

	host := normalizeHost(cfg.Host)
	canonicalFingerprint, err := c.canonicalize(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fingerprint kubeconfig for %s: %w", host, err)
	}
	key := restMapperKey{host: host, fingerprint: canonicalFingerprint}

	c.mu.Lock()
	if mapper, httpClient, ok := c.lookupLocked(key, rawFingerprint, now); ok {
		c.mu.Unlock()
		return mapper, httpClient, nil
	}
	c.mu.Unlock()

	cfg = rest.CopyConfig(cfg)
	if cfg.UserAgent == "" {
		cfg.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HTTP client for %s: %w", host, err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create REST mapper for %s: %w", host, err)
	}

	// Re-check under the lock: concurrent lookups with equivalent kubeconfigs
	// must converge on one mapper, or each would run its own discovery. Distinct
	// identities use distinct keys, so a store here can never clobber another
	// identity's mapper. The loser returns the winner's pair, not a mix.
	c.mu.Lock()
	defer c.mu.Unlock()
	if winnerMapper, winnerClient, ok := c.lookupLocked(key, rawFingerprint, now); ok {
		return winnerMapper, winnerClient, nil
	}

	// An aged predecessor keeps its slot's key but not its alias: the new entry
	// resolves from the bytes that built it.
	if old, ok := c.entries[key]; ok {
		delete(c.aliases, old.rawFingerprint)
	}
	e := &restMapperEntry{mapper: mapper, httpClient: httpClient, rawFingerprint: rawFingerprint, createdAt: now}
	e.lastUsed = now.UnixNano()
	c.entries[key] = e
	c.aliases[rawFingerprint] = key

	// Inserts are rare (a new cluster, a rotation, an age refresh) so an O(n)
	// scan for the least-recently-used entry beats maintaining LRU bookkeeping
	// on every hit.
	for len(c.entries) > c.maxEntries {
		c.evictOldestLocked()
	}

	return mapper, httpClient, nil
}

// evictOldestLocked removes the least-recently-used entry and its alias. Must
// be called under the lock.
func (c *restMapperCache) evictOldestLocked() {
	var (
		oldestKey restMapperKey
		oldest    int64
		found     bool
	)
	for key, e := range c.entries {
		if !found || e.lastUsed < oldest {
			found = true
			oldestKey, oldest = key, e.lastUsed
		}
	}
	if found {
		delete(c.aliases, c.entries[oldestKey].rawFingerprint)
		delete(c.entries, oldestKey)
	}
}

// lookupLocked returns the live pair for key, refreshing the entry's lastUsed
// and promoting rawFingerprint to its current byte representation. Both the
// canonical-hit path and the post-build re-check converge through it, so a
// caller that lost a build race receives the winning entry's mapper and
// transport together. Must be called under the lock.
func (c *restMapperCache) lookupLocked(key restMapperKey, rawFingerprint [sha256.Size]byte, now time.Time) (meta.RESTMapper, *http.Client, bool) {
	entry, ok := c.entries[key]
	if !ok || entry.aged(now, c.refreshInterval) {
		return nil, nil, false
	}
	entry.lastUsed = now.UnixNano()
	c.promote(entry, key, rawFingerprint)

	return entry.mapper, entry.httpClient, true
}

// promote makes rawFingerprint the entry's current byte representation, so its
// later lookups take the fast path instead of canonicalizing again. The
// previous representation's alias is dropped rather than kept: retaining every
// representation ever seen would grow the index without bound on repeated
// Secret rewrites. Must be called under the lock.
func (c *restMapperCache) promote(entry *restMapperEntry, key restMapperKey, rawFingerprint [sha256.Size]byte) {
	if entry.rawFingerprint == rawFingerprint {
		return
	}
	delete(c.aliases, entry.rawFingerprint)
	entry.rawFingerprint = rawFingerprint
	c.aliases[rawFingerprint] = key
}

// fingerprint digests raw bytes into a comparable array, so it can serve as a
// map key directly without a hex allocation on every lookup.
func fingerprint(data []byte) [sha256.Size]byte {
	return sha256.Sum256(data)
}

func canonicalKubeconfigFingerprint(kubeconfig []byte) ([sha256.Size]byte, error) {
	config, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	for _, cluster := range config.Clusters {
		cluster.Server = normalizeHost(cluster.Server)
	}
	canonical, err := clientcmd.Write(*config)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return fingerprint(canonical), nil
}

// RESTMapperCacheSweeper returns the runnable that expires idle entries of the
// shared RESTMapper cache.
func RESTMapperCacheSweeper() manager.Runnable {
	return &restMapperSweeper{cache: sharedRESTMapperCache}
}

// restMapperSweeper evicts idle entries on a fixed cadence, so that a fleet
// that only shrinks releases its mappers, discovery data, transports, and
// credential material even when no client is ever built again.
type restMapperSweeper struct {
	cache *restMapperCache
}

func (s *restMapperSweeper) Start(ctx context.Context) error {
	ticker := time.NewTicker(s.cache.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.cache.evictStale(s.cache.nowFunc())
		}
	}
}

func (*restMapperSweeper) NeedLeaderElection() bool { return false }

func (c *restMapperCache) evictStale(now time.Time) {
	cutoff := now.Add(-c.ttl).UnixNano()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		if e.lastUsed < cutoff {
			delete(c.entries, key)
			delete(c.aliases, e.rawFingerprint)
		}
	}
}

func (c *restMapperCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
