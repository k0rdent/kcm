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

package rbac

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReachTracker wraps a child cluster client and records whether any request it served got a
// response from the API server. Whether anything changed says nothing about that: a no-op Get or
// a List that finds nothing to delete is a response too, and a failing write may follow a
// successful one in the same call. Not safe for concurrent use.
type ReachTracker struct {
	client.Client
	reached bool
}

// TrackReach wraps cl in a [ReachTracker].
func TrackReach(cl client.Client) *ReachTracker {
	return &ReachTracker{Client: cl}
}

// Reached reports whether any request so far got a response, an error status included.
func (t *ReachTracker) Reached() bool {
	return t.reached
}

func (t *ReachTracker) observe(err error) error {
	var status apierrors.APIStatus
	if err == nil || errors.As(err, &status) {
		t.reached = true
	}
	return err
}

func (t *ReachTracker) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return t.observe(t.Client.Get(ctx, key, obj, opts...))
}

func (t *ReachTracker) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return t.observe(t.Client.List(ctx, list, opts...))
}

func (t *ReachTracker) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return t.observe(t.Client.Apply(ctx, obj, opts...))
}

func (t *ReachTracker) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return t.observe(t.Client.Create(ctx, obj, opts...))
}

func (t *ReachTracker) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return t.observe(t.Client.Delete(ctx, obj, opts...))
}

func (t *ReachTracker) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return t.observe(t.Client.Update(ctx, obj, opts...))
}

func (t *ReachTracker) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return t.observe(t.Client.Patch(ctx, obj, patch, opts...))
}

func (t *ReachTracker) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return t.observe(t.Client.DeleteAllOf(ctx, obj, opts...))
}
