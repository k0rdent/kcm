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

// restMapperCache holds one RESTMapper per remote cluster. A client.New that
// leaves Options.Mapper unset gets a dynamic mapper of its own, and each fresh
// mapper discovers the target apiserver's API surface on its first RESTMapping
// — two requests, one of which carries every group on a server supporting
// aggregated discovery.
//
// Keyed by apiserver URL: the API surface is a property of the cluster, not of
// the credentials used to reach it, so a credential rotation replaces an entry
// rather than adding one. The fingerprint is of the kubeconfig the mapper was
// built from, because a mapper owns a discovery client with those credentials
// baked in, so a rotation has to produce a new one.
type restMapperCache struct {
	entries map[string]*restMapperEntry
	nowFunc func() time.Time
	// raceHook runs between examining the cache and storing a new mapper. Nil
	// outside tests, which use it to interleave a competing lookup. Read under
	// mu, so a test may swap it mid-flight without racing a concurrent get.
	raceHook func()

	ttl           time.Duration
	sweepInterval time.Duration

	// generation increments on every store, so a build that started earlier can
	// tell it is about to overwrite state a later caller installed.
	generation uint64

	lastSweep atomic.Int64
	mu        sync.RWMutex
}

func newRESTMapperCache(ttl, sweepInterval time.Duration) *restMapperCache {
	return newRESTMapperCacheWithClock(ttl, sweepInterval, time.Now)
}

func newRESTMapperCacheWithClock(ttl, sweepInterval time.Duration, nowFunc func() time.Time) *restMapperCache {
	return &restMapperCache{
		entries:       make(map[string]*restMapperEntry),
		nowFunc:       nowFunc,
		ttl:           ttl,
		sweepInterval: sweepInterval,
	}
}

type restMapperEntry struct {
	mapper               meta.RESTMapper
	fingerprint          string
	canonicalFingerprint string
	generation           uint64
	lastUsed             atomic.Int64
}

const (
	// restMapperTTL is how long an unused mapper is kept, so that deleted
	// clusters do not retain a discovery cache for the life of the process.
	restMapperTTL = time.Hour

	// restMapperSweepInterval is how often eviction runs. It is driven by elapsed
	// time rather than by cache misses: deleting a cluster produces no miss, so a
	// miss-driven sweep would never reclaim a fleet that only shrinks.
	restMapperSweepInterval = 10 * time.Minute
)

var sharedRESTMapperCache = newRESTMapperCache(restMapperTTL, restMapperSweepInterval)

func normalizeHost(host string) string { return strings.TrimRight(host, "/") }

func (c *restMapperCache) get(cfg *rest.Config, kubeconfig []byte) (meta.RESTMapper, error) {
	now := c.nowFunc()
	c.maybeSweep(now)
	host := normalizeHost(cfg.Host)
	fingerprint := fingerprint(kubeconfig)

	c.mu.RLock()
	entry, ok := c.entries[host]
	if ok && entry.fingerprint == fingerprint {
		entry.lastUsed.Store(now.UnixNano())
		c.mu.RUnlock()
		return entry.mapper, nil
	}
	c.mu.RUnlock()

	var (
		canonicalFingerprint string
		observedGeneration   uint64
		err                  error
	)
	if ok {
		canonicalFingerprint, err = canonicalKubeconfigFingerprint(kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to fingerprint kubeconfig for %s: %w", host, err)
		}

		c.mu.RLock()
		entry, ok = c.entries[host]
		if ok {
			observedGeneration = entry.generation
			if entry.canonicalFingerprint == canonicalFingerprint {
				entry.lastUsed.Store(now.UnixNano())
				c.mu.RUnlock()
				return entry.mapper, nil
			}
		}
		c.mu.RUnlock()
	}

	c.mu.RLock()
	raceHook := c.raceHook
	c.mu.RUnlock()
	if raceHook != nil {
		raceHook()
	}

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
	cur, ok := c.entries[host]
	if ok && (cur.fingerprint == fingerprint || cur.canonicalFingerprint == canonicalFingerprint) {
		cur.lastUsed.Store(now.UnixNano())
		return cur.mapper, nil
	}
	if ok && cur.generation != observedGeneration {
		return mapper, nil
	}

	if canonicalFingerprint == "" {
		if canonicalFingerprint, err = canonicalKubeconfigFingerprint(kubeconfig); err != nil {
			return nil, fmt.Errorf("failed to fingerprint kubeconfig for %s: %w", host, err)
		}
	}

	c.generation++
	e := &restMapperEntry{
		mapper:               mapper,
		fingerprint:          fingerprint,
		canonicalFingerprint: canonicalFingerprint,
		generation:           c.generation,
	}
	e.lastUsed.Store(now.UnixNano())
	c.entries[host] = e

	return mapper, nil
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

// setRaceHook installs the hook under mu, so tests can swap it without racing a
// concurrent get.
func (c *restMapperCache) setRaceHook(hook func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raceHook = hook
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
	for host, e := range c.entries {
		if e.lastUsed.Load() < cutoff {
			delete(c.entries, host)
		}
	}
}

func (c *restMapperCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
