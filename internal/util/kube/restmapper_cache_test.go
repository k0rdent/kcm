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
	"testing"

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
	newCache := func() *restMapperCache {
		return &restMapperCache{entries: make(map[string]restMapperEntry)}
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
		c := newCache()
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		first := get(t, c, kubeconfig)
		for range 10 {
			require.Same(t, first, get(t, c, kubeconfig), "a cached mapper must be reused verbatim")
		}
		require.Equal(t, 1, c.len())
	})

	t.Run("rotated credentials replace the mapper", func(t *testing.T) {
		c := newCache()
		host := "https://a.example:6443"

		first := get(t, c, kubeconfigForHost(t, host, "u"))
		second := get(t, c, kubeconfigForHost(t, host, "rotated"))

		// A mapper owns a discovery client with the old credentials baked in,
		// so reusing it after a rotation would fail on the next refresh.
		require.NotSame(t, first, second, "rotated credentials must rebuild the mapper")
		require.Equal(t, 1, c.len(), "rotation must replace the entry, not add one")
	})

	t.Run("distinct clusters get distinct mappers", func(t *testing.T) {
		c := newCache()

		a := get(t, c, kubeconfigForHost(t, "https://a.example:6443", "u"))
		b := get(t, c, kubeconfigForHost(t, "https://b.example:6443", "u"))

		require.NotSame(t, a, b)
		require.Equal(t, 2, c.len())
	})

	t.Run("concurrent lookups converge on one mapper", func(t *testing.T) {
		c := newCache()
		kubeconfig := kubeconfigForHost(t, "https://a.example:6443", "u")

		const goroutines = 50
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			mappers []any
		)
		wg.Add(goroutines)
		for range goroutines {
			go func() {
				defer wg.Done()
				cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
				if err != nil {
					return
				}
				m, err := c.get(cfg, kubeconfig)
				if err != nil {
					return
				}
				mu.Lock()
				mappers = append(mappers, m)
				mu.Unlock()
			}()
		}
		wg.Wait()

		require.Len(t, mappers, goroutines)
		for _, m := range mappers {
			require.Same(t, mappers[0], m, "all concurrent callers must receive the same mapper")
		}
		require.Equal(t, 1, c.len())
	})
}
