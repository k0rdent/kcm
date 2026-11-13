// Copyright 2025
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

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultPageSize is the number of objects fetched per page by [EachPage].
const DefaultPageSize = 500

// cachedReaderContinue is the continue token the manager's cache sets on every list it serves,
// see sigs.k8s.io/controller-runtime/pkg/cache/internal.CacheReader.List.
const cachedReaderContinue = "continue-not-supported"

// EachPage lists objects in chunks and calls handle once per page, stopping as soon as
// handle returns false. The list given to handle is valid only until the next page is
// fetched, so anything to be kept must be copied out.
//
// Only the API server serves the continue option, so paging pays off solely for an uncached
// reader (a regional or child cluster client, an APIReader). A cached reader is detected
// before anything reaches handle and is then served in a single unlimited list, which is what
// the cache does anyway: correct, just without the benefit of paging.
//
// A Limit among the options is therefore the page size, not a cap on the objects handled.
//
// An expired continue token (410 Gone) is returned as is: every caller either retries the
// whole read or re-runs on the next poll.
func EachPage[L client.ObjectList](ctx context.Context, r client.Reader, newList func() L, handle func(L) (bool, error), opts ...client.ListOption) error {
	listOpts := &client.ListOptions{}
	listOpts.ApplyOptions(opts)
	if listOpts.Limit <= 0 {
		listOpts.Limit = DefaultPageSize
	}

	cached := false
	for {
		list := newList()
		if err := r.List(ctx, list, listOpts); err != nil {
			return err
		}

		// The cache marks every answer with this token and truncates at Limit, so the
		// objects are only complete once it has been asked again without a limit. Checked
		// before handle runs, otherwise a truncated page would reach it first.
		if !cached && list.GetContinue() == cachedReaderContinue {
			cached, listOpts.Limit = true, 0
			continue
		}

		proceed, err := handle(list)
		if err != nil || !proceed {
			return err
		}

		if cached {
			return nil // everything was served in one go
		}

		listOpts.Continue = list.GetContinue()
		if listOpts.Continue == "" {
			return nil
		}
	}
}
