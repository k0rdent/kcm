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
		return newRESTMapperCache(restMapperTTL, restMapperRefreshInterval, restMapperSweepInterval)
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

	t.Run("rotated credentials replace the mapper", func(t *testing.T) {
		c := newCache(t)
		host := "https://a.example:6443"

		first := get(t, c, kubeconfigForHost(t, host, "u"))
		second := get(t, c, kubeconfigForHost(t, host, "rotated"))

		// A mapper owns a discovery client with the old credentials baked in,
		// so reusing it after a rotation would fail on the next refresh.
		require.NotSame(t, first, second, "rotated credentials must rebuild the mapper")
		require.Equal(t, 1, c.len(), "rotation must replace the entry, not add one")
	})

	t.Run("distinct clusters get distinct mappers", func(t *testing.T) {
		c := newCache(t)

		a := get(t, c, kubeconfigForHost(t, "https://a.example:6443", "u"))
		b := get(t, c, kubeconfigForHost(t, "https://b.example:6443", "u"))

		require.NotSame(t, a, b)
		require.Equal(t, 2, c.len())
	})

	t.Run("an idle cluster is evicted while an active one survives", func(t *testing.T) {
		// Eviction policy is asserted by calling the sweep directly, with the
		// sweep interval set beyond the test's lifetime so no lookup triggers one
		// behind our back. Nothing here depends on wall-clock time.
		const ttl = time.Minute

		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(ttl, time.Hour, time.Hour, func() time.Time {
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

	t.Run("a hit alone reclaims an idle cluster", func(t *testing.T) {
		// The final lookup has to be a genuine hit, which is what require.Same
		// checks: asserting on c.len() alone would also pass if the sweep evicted
		// the live entry and the lookup simply rebuilt it.
		const ttl = time.Minute

		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(ttl, time.Hour, ttl/10, func() time.Time {
			return time.Unix(0, clock.Load())
		})

		live := kubeconfigForHost(t, "https://live.example:6443", "u")
		gone := kubeconfigForHost(t, "https://gone.example:6443", "u")

		first := get(t, c, live)
		get(t, c, gone)
		require.Equal(t, 2, c.len())

		clock.Store(int64(ttl / 10 * 9))
		require.Same(t, first, get(t, c, live), "no entry should have aged out yet")
		require.Equal(t, 2, c.len())

		clock.Store(int64(ttl / 2 * 3))
		again := get(t, c, live)

		require.Same(t, first, again, "the live entry must be a hit, not a rebuild")
		require.Equal(t, 1, c.len(), "a hit alone should have swept the idle entry")
	})

	t.Run("an aged mapper is rebuilt even while in constant use", func(t *testing.T) {
		// Guards against constant use pinning a mapper forever: the dynamic
		// mapper re-discovers only on a NoMatch, so a mapping it already knows
		// would otherwise survive API removals until the process restarts. Every
		// hit below refreshes lastUsed, so only createdAt can trigger the
		// rebuild.
		const refresh = time.Minute

		var clock atomic.Int64
		c := newRESTMapperCacheWithClock(time.Hour, refresh, time.Hour, func() time.Time {
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

	t.Run("a slow build does not overwrite newer credentials", func(t *testing.T) {
		c := newCache(t)
		host := "https://a.example:6443"

		old := kubeconfigForHost(t, host, "old")
		fresh := kubeconfigForHost(t, host, "fresh")

		var newest any
		// One-shot: cleared before the nested lookup so it does not re-enter.
		c.setRaceHook(func() {
			c.setRaceHook(nil)
			newest = get(t, c, fresh)
		})

		oldCfg, err := clientcmd.RESTConfigFromKubeConfig(old)
		require.NoError(t, err)
		stale, err := c.get(oldCfg, old)
		require.NoError(t, err)
		require.NotNil(t, stale, "the losing caller still gets a usable mapper")
		require.NotSame(t, newest, stale, "it uses its own credentials, not the cached ones")

		require.Equal(t, 1, c.len())
		require.Same(t, newest, get(t, c, fresh),
			"the cached entry must still be the one built from the newer kubeconfig")
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
