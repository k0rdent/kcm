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
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// restMapperCache holds one RESTMapper per remote cluster. A client.New that
// leaves Options.Mapper unset gets a dynamic mapper of its own, and each fresh
// mapper discovers the target apiserver's API surface on its first RESTMapping
// — two requests, one of which carries every group on a server supporting
// aggregated discovery.
//
// Keyed by apiserver URL: the API surface is a property of the cluster, not of
// the credentials used to reach it, so this also bounds the map by cluster count
// rather than growing on every credential rotation. The fingerprint is of the
// kubeconfig the mapper was built from, because a mapper owns a discovery client with
// those credentials baked in, so a rotation has to produce a new one.
type restMapperCache struct {
	entries map[string]restMapperEntry
	mu      sync.RWMutex
}

type restMapperEntry struct {
	mapper      meta.RESTMapper
	fingerprint string
}

var sharedRESTMapperCache = &restMapperCache{entries: make(map[string]restMapperEntry)}

func (c *restMapperCache) get(cfg *rest.Config, kubeconfig []byte) (meta.RESTMapper, error) {
	sum := sha256.Sum256(kubeconfig)
	fingerprint := hex.EncodeToString(sum[:])

	c.mu.RLock()
	entry, ok := c.entries[cfg.Host]
	c.mu.RUnlock()
	if ok && entry.fingerprint == fingerprint {
		return entry.mapper, nil
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST mapper: %w", err)
	}

	// Re-check under the write lock and prefer whatever a racing caller stored:
	// callers must converge on one mapper per cluster, or several would each run
	// their own discovery.
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.entries[cfg.Host]; ok && cur.fingerprint == fingerprint {
		return cur.mapper, nil
	}
	c.entries[cfg.Host] = restMapperEntry{mapper: mapper, fingerprint: fingerprint}

	return mapper, nil
}

func (c *restMapperCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
