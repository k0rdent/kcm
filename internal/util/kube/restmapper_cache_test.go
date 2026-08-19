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
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestRESTMapperCacheMaxEntriesFromEnv(t *testing.T) {
	// The override guards against a fleet larger than the default cap turning
	// every poll tick into an eviction cycle; anything that does not parse to a
	// positive integer falls back to the compiled-in default.
	for value, want := range map[string]int{
		"1024":                  1024,
		"1":                     1,
		"":                      restMapperMaxEntries,
		"0":                     restMapperMaxEntries,
		"-5":                    restMapperMaxEntries,
		"many":                  restMapperMaxEntries,
		"256.5":                 restMapperMaxEntries,
		"512x":                  restMapperMaxEntries,
		"1e3":                   restMapperMaxEntries,
		"1000000":               restMapperMaxConfigurableEntries,
		"1000001":               restMapperMaxConfigurableEntries,
		"9999999":               restMapperMaxConfigurableEntries,
		"99999999999999999999":  restMapperMaxConfigurableEntries,
		"-99999999999999999999": restMapperMaxEntries,
	} {
		t.Run("value "+value, func(t *testing.T) {
			t.Setenv(restMapperCacheMaxEntriesEnvName, value)
			require.Equal(t, want, restMapperCacheMaxEntriesFromEnv())
		})
	}

	t.Run("unset", func(t *testing.T) {
		t.Setenv(restMapperCacheMaxEntriesEnvName, "sentinel") // registers restore of the prior value
		require.NoError(t, os.Unsetenv(restMapperCacheMaxEntriesEnvName))
		require.Equal(t, restMapperMaxEntries, restMapperCacheMaxEntriesFromEnv())
	})
}

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

	getPair := func(t *testing.T, c *restMapperCache, kubeconfig []byte) (any, *http.Client) {
		t.Helper()
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
		require.NoError(t, err)
		m, httpClient, err := c.get(cfg, kubeconfig)
		require.NoError(t, err)
		require.NotNil(t, m)
		require.NotNil(t, httpClient)

		return m, httpClient
	}

	get := func(t *testing.T, c *restMapperCache, kubeconfig []byte) any {
		t.Helper()
		m, _ := getPair(t, c, kubeconfig)

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
		c.canonicalize = func(kubeconfig []byte) ([sha256.Size]byte, error) {
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

	t.Run("the object client shares the mapper's HTTP client", func(t *testing.T) {
		c := newCache(t)
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		firstMapper, firstClient := getPair(t, c, kubeconfig)
		againMapper, againClient := getPair(t, c, kubeconfig)
		require.Same(t, firstMapper, againMapper)
		require.Same(t, firstClient, againClient, "repeated lookups must reuse the cached HTTP client")

		// A rotation gets its own pair: the new mapper must never ride the old
		// credentials' transport, nor the other way around.
		rotatedMapper, rotatedClient := getPair(t, c, kubeconfigForHost(t, "https://a.example:6443", "rotated"))
		require.NotSame(t, firstMapper, rotatedMapper)
		require.NotSame(t, firstClient, rotatedClient, "rotated credentials must get their own transport")
	})

	t.Run("the cached HTTP client sends the default Kubernetes user agent", func(t *testing.T) {
		// The User-Agent is baked into the transport at build time: client-go
		// adds its UA round tripper only if cfg.UserAgent is set at that moment,
		// and controller-runtime's own defaulting never reaches a client that is
		// handed to it prebuilt. Requests must not fall back to Go's default UA.
		var got atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got.Store(r.Header.Get("User-Agent"))
		}))
		defer srv.Close()

		c := newCache(t)
		_, httpClient := getPair(t, c, kubeconfigForHost(t, srv.URL, "u"))

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, rest.DefaultKubernetesUserAgent(), got.Load(),
			"requests through the cached client must identify themselves as this process")
	})

	t.Run("a lookup losing the build race converges on the winner's pair", func(t *testing.T) {
		// Deterministic version of the concurrent test below: the injected
		// canonicalizer runs between the fast path and the locked re-check, so a
		// competing store placed there is guaranteed to win the race, and the
		// outer lookup must return the winner's mapper and transport rather than
		// the pair it would have built.
		c := newCache(t)
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		var (
			winnerMapper any
			winnerClient *http.Client
			raced        bool
		)
		c.canonicalize = func(kc []byte) ([sha256.Size]byte, error) {
			if !raced {
				raced = true
				winnerMapper, winnerClient = getPair(t, c, kc)
			}
			return canonicalKubeconfigFingerprint(kc)
		}

		loserMapper, loserClient := getPair(t, c, kubeconfig)

		require.Same(t, winnerMapper, loserMapper, "the loser must adopt the winner's mapper")
		require.Same(t, winnerClient, loserClient, "the loser must adopt the winner's transport, not its own build")
		require.Equal(t, 1, c.len())
	})

	t.Run("concurrent lookups converge on one mapper", func(t *testing.T) {
		c := newCache(t)
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		type pair struct {
			mapper     any
			httpClient *http.Client
		}
		const goroutines = 50
		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			pairs []pair
			errs  []error
		)
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
				if err == nil {
					var (
						m any
						h *http.Client
					)
					m, h, err = c.get(cfg, kubeconfig)
					if err == nil {
						mu.Lock()
						pairs = append(pairs, pair{mapper: m, httpClient: h})
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
		require.Len(t, pairs, goroutines)
		for _, p := range pairs {
			require.Same(t, pairs[0].mapper, p.mapper, "all concurrent callers must receive the same mapper")
			require.Same(t, pairs[0].httpClient, p.httpClient,
				"build-race losers must return the winning entry's transport, not their own")
		}
		require.Equal(t, 1, c.len())
	})
}
