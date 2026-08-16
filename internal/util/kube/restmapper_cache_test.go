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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
)

// kubeconfigForHost builds a kubeconfig for host, authenticating as user. The
// mapper is lazy, so nothing is ever contacted; only the identity of the
// resulting rest.Config matters here.
func kubeconfigForHost(t *testing.T, host, user string) []byte {
	t.Helper()

	return []byte(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: ` + host + `
contexts:
- name: c
  context:
    cluster: c
    user: ` + user + `
current-context: c
users:
- name: ` + user + `
  user:
    token: ` + user + `-token
`)
}

func TestRESTMapperCache(t *testing.T) {
	newCache := func(t *testing.T) *restMapperCache {
		t.Helper()
		return newRESTMapperCache(restMapperTTL, restMapperRefreshInterval, restMapperSweepInterval, restMapperMaxEntries)
	}

	get := func(t *testing.T, c *restMapperCache, kubeconfig []byte) any {
		t.Helper()
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
		require.NoError(t, err)
		m, err := c.get(cfg, kubeconfig)
		require.NoError(t, err)
		require.NotNil(t, m)

		return m
	}

	t.Run("same cluster reuses one mapper", func(t *testing.T) {
		c := newCache(t)
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		first := get(t, c, kubeconfig)
		for range 10 {
			require.Same(t, first, get(t, c, kubeconfig), "a cached mapper must be reused verbatim")
		}
		require.Equal(t, 1, c.len())
	})

	t.Run("equivalent server URLs reuse one mapper", func(t *testing.T) {
		c := newCache(t)

		first := get(t, c, kubeconfigForHost(t, "https://a.example:6443", "u"))
		second := get(t, c, kubeconfigForHost(t, "https://a.example:6443/", "u"))

		require.Same(t, first, second)
		require.Equal(t, 1, c.len())
	})

	t.Run("rotated credentials rebuild the mapper and the old entry idles out", func(t *testing.T) {
		const ttl = time.Minute

		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(ttl, time.Hour, time.Hour, restMapperMaxEntries, func() time.Time {
			return time.Unix(0, clock.Load())
		})
		host := "https://a.example:6443"

		// A mapper owns a discovery client with the old credentials baked in,
		// so reusing it after a rotation would fail on the next refresh.
		first := get(t, c, kubeconfigForHost(t, host, "u"))
		second := get(t, c, kubeconfigForHost(t, host, "rotated"))
		require.NotSame(t, first, second, "rotated credentials must rebuild the mapper")
		require.Equal(t, 2, c.len(), "the superseded identity is kept until it idles out")

		// Only the rotated identity is looked up from now on; the superseded
		// entry ages past the TTL and is reclaimed by the sweep.
		clock.Store(int64(2 * ttl))
		require.Same(t, second, get(t, c, kubeconfigForHost(t, host, "rotated")))
		c.evictStale(time.Unix(0, clock.Load()))
		require.Equal(t, 1, c.len(), "the superseded identity must be swept once idle")
	})

	t.Run("two identities on one host keep their own mappers", func(t *testing.T) {
		c := newCache(t)
		host := "https://a.example:6443"
		alice := kubeconfigForHost(t, host, "alice")
		bob := kubeconfigForHost(t, host, "bob")

		first := get(t, c, alice)
		second := get(t, c, bob)
		require.NotSame(t, first, second, "identities must not share a credentialed discovery client")

		// Alternating identities must not evict each other: each keeps hitting
		// the mapper it started with, without a single rebuild.
		for range 5 {
			require.Same(t, first, get(t, c, alice))
			require.Same(t, second, get(t, c, bob))
		}
		require.Equal(t, 2, c.len())
	})

	t.Run("a rewritten kubeconfig canonicalizes once and swaps the alias", func(t *testing.T) {
		c := newCache(t)
		var canonicalizations atomic.Int64
		c.canonicalize = func(kubeconfig []byte) (string, error) {
			canonicalizations.Add(1)
			return canonicalKubeconfigFingerprint(kubeconfig)
		}

		// Three byte representations of one identity, arriving one after the
		// other the way Secret rewrites do. Each must canonicalize exactly once
		// — on arrival — and then ride the raw fast path.
		first := get(t, c, kubeconfigForHost(t, "https://a.example:6443", "u"))
		for step, kubeconfig := range [][]byte{
			kubeconfigForHost(t, "https://a.example:6443", "u"),
			kubeconfigForHost(t, "https://a.example:6443/", "u"),
			kubeconfigForHost(t, "https://a.example:6443//", "u"),
		} {
			for range 5 {
				require.Same(t, first, get(t, c, kubeconfig),
					"an equivalent representation must resolve to the existing mapper")
			}
			require.EqualValues(t, step+1, canonicalizations.Load(),
				"only a representation's first lookup may canonicalize")
		}

		// Promotion swaps the entry's alias rather than accumulating one per
		// representation, so the index stays pinned to the entry count.
		require.Equal(t, 1, c.len())
		require.Len(t, c.aliases, 1, "promotion must swap the alias, not retain old representations")
	})

	t.Run("distinct clusters get distinct mappers", func(t *testing.T) {
		c := newCache(t)

		a := get(t, c, kubeconfigForHost(t, "https://a.example:6443", "u"))
		b := get(t, c, kubeconfigForHost(t, "https://b.example:6443", "u"))

		require.NotSame(t, a, b)
		require.Equal(t, 2, c.len())
	})

	t.Run("an idle cluster is evicted while an active one survives", func(t *testing.T) {
		// Asserts the eviction policy by calling the sweep directly, the way the
		// sweeper runnable does. Nothing here depends on wall-clock time.
		const ttl = time.Minute

		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(ttl, time.Hour, time.Hour, restMapperMaxEntries, func() time.Time {
			return time.Unix(0, clock.Load())
		})

		live := kubeconfigForHost(t, "https://live.example:6443", "u")
		gone := kubeconfigForHost(t, "https://gone.example:6443", "u")

		first := get(t, c, live)
		get(t, c, gone)
		require.Equal(t, 2, c.len())

		clock.Store(int64(2 * ttl))
		again := get(t, c, live)
		c.evictStale(time.Unix(0, clock.Load()))

		require.Equal(t, 1, c.len(), "the idle entry should have been evicted")
		require.Same(t, first, again, "the in-use entry must survive the sweep")
	})

	t.Run("the sweeper reclaims idle entries without any lookup", func(t *testing.T) {
		// A deleted cluster produces no further lookups, so expiry must not
		// depend on one: after the last get below, only the running sweeper
		// touches the cache. The injected clock jumps past the TTL; the ticker
		// merely has to fire, so its interval is a real millisecond.
		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(time.Minute, time.Hour, time.Millisecond, restMapperMaxEntries, func() time.Time {
			return time.Unix(0, clock.Load())
		})

		get(t, c, kubeconfigForHost(t, "https://gone.example:6443", "u"))
		require.Equal(t, 1, c.len())
		clock.Store(int64(2 * time.Minute))

		sweeper := &restMapperSweeper{cache: c}
		require.False(t, sweeper.NeedLeaderElection(),
			"every replica builds clients, so every replica must sweep")

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- sweeper.Start(ctx) }()

		require.Eventually(t, func() bool { return c.len() == 0 }, 10*time.Second, time.Millisecond,
			"the sweeper must reclaim the idle entry with no lookup driving it")

		cancel()
		require.NoError(t, <-done, "the sweeper must stop cleanly on context cancellation")
		require.Empty(t, c.aliases, "the entry's alias must be reclaimed with it")
	})

	t.Run("inserting past capacity evicts the least recently used", func(t *testing.T) {
		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(time.Hour, time.Hour, time.Hour, 2, func() time.Time {
			return time.Unix(0, clock.Load())
		})

		a := kubeconfigForHost(t, "https://a.example:6443", "u")
		b := kubeconfigForHost(t, "https://b.example:6443", "u")
		d := kubeconfigForHost(t, "https://d.example:6443", "u")

		first := get(t, c, a)
		clock.Store(1)
		second := get(t, c, b)
		clock.Store(2)
		require.Same(t, first, get(t, c, a)) // a is now more recently used than b

		clock.Store(3)
		third := get(t, c, d) // over capacity: b is the LRU entry and must go
		require.Equal(t, 2, c.len())
		require.Len(t, c.aliases, 2, "the evicted entry's alias must go with it")

		require.Same(t, first, get(t, c, a), "a recently used entry must survive the cap")
		require.Same(t, third, get(t, c, d), "the newest entry must survive the cap")
		require.NotSame(t, second, get(t, c, b), "the evicted identity must rebuild on return")
	})

	t.Run("an aged mapper is rebuilt even while in constant use", func(t *testing.T) {
		// Guards against constant use pinning a mapper forever: the dynamic
		// mapper re-discovers only on a NoMatch, so a mapping it already knows
		// would otherwise survive API removals until the process restarts. Every
		// hit below refreshes lastUsed, so only createdAt can trigger the
		// rebuild.
		const refresh = time.Minute

		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(time.Hour, refresh, time.Hour, restMapperMaxEntries, func() time.Time {
			return time.Unix(0, clock.Load())
		})
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		first := get(t, c, kubeconfig)
		for i := range 9 {
			clock.Store(int64(refresh) / 10 * int64(i+1))
			require.Same(t, first, get(t, c, kubeconfig),
				"a mapper younger than the refresh interval must be reused")
		}

		clock.Store(int64(refresh))
		second := get(t, c, kubeconfig)
		require.NotSame(t, first, second, "an aged mapper must be rebuilt despite recent use")
		require.Equal(t, 1, c.len(), "the rebuild must replace the entry, not add one")

		// The replacement's age starts at its own build time.
		require.Same(t, second, get(t, c, kubeconfig))
	})

	t.Run("concurrent lookups converge on one mapper", func(t *testing.T) {
		c := newCache(t)
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		const goroutines = 50
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			mappers []any
			errs    []error
		)
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
				if err == nil {
					var m any
					m, err = c.get(cfg, kubeconfig)
					if err == nil {
						mu.Lock()
						mappers = append(mappers, m)
						mu.Unlock()
						return
					}
				}
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}()
		}
		wg.Wait()

		require.Empty(t, errs, "no concurrent lookup should fail")
		require.Len(t, mappers, goroutines)
		for _, m := range mappers {
			require.Same(t, mappers[0], m, "all concurrent callers must receive the same mapper")
		}
		require.Equal(t, 1, c.len())
	})
}
