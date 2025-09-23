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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeletionExcludeFn returns true for objects that must be preserved.
type DeletionExcludeFn[T client.Object] func(T) bool

// DeleteAllExceptAndWait deletes collection of the given object
// across all of the namespaces available, and wait for the given
// timeout to ensure all of the objects have actually been deleted.
func DeleteAllExceptAndWait[T client.Object](
	ctx context.Context,
	c client.Client,
	obj T,
	list client.ObjectList,
	exclude DeletionExcludeFn[T],
	timeout time.Duration,
	extraOpts ...client.DeleteAllOfOption,
) error {
	namespaces := new(corev1.NamespaceList)
	if err := c.List(ctx, namespaces); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	for _, ns := range namespaces.Items {
		if exclude == nil {
			opts := append([]client.DeleteAllOfOption{client.InNamespace(ns.Name)}, extraOpts...)
			if err := c.DeleteAllOf(ctx, obj, opts...); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete collection: %w", err)
			}

			continue
		}

		if err := c.List(ctx, list, client.InNamespace(ns.Name)); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to list in %q: %w", ns.Name, err)
		}

		items, err := apimeta.ExtractList(list)
		if err != nil {
			return fmt.Errorf("failed to extract list: %w", err)
		}

		for _, v := range items {
			o, ok := v.(T)
			if !ok {
				return fmt.Errorf("unexpected list item type %T", v)
			}

			if exclude != nil && exclude(o) {
				continue
			}

			if err := c.Delete(ctx, o); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete %s %s: %w", o.GetObjectKind().GroupVersionKind().String(), client.ObjectKeyFromObject(o), err)
			}
		}
	}

	interval := 500 * time.Millisecond
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		if err := c.List(ctx, list); client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("failed to list during wait: %w", err)
		}

		objs, err := apimeta.ExtractList(list)
		if err != nil {
			return false, fmt.Errorf("failed to extract list during wait: %w", err)
		}

		for _, v := range objs {
			o, ok := v.(T)
			if !ok {
				return true, fmt.Errorf("unexpected list item type %T", v) // NOTE: return without wait
			}

			if exclude != nil && exclude(o) {
				continue
			}

			return false, nil
		}

		return true, nil
	})
}
