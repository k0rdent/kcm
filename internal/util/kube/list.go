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

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ExistsAny lists with Limit=1 and returns true if any item exists.
func ExistsAny(ctx context.Context, c client.Client, list client.ObjectList, opts ...client.ListOption) (bool, error) {
	lo := []client.ListOption{client.Limit(1)}
	lo = append(lo, opts...)

	if err := c.List(ctx, list, lo...); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return meta.LenList(list) > 0, nil
}
