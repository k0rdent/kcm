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
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// pagedReader serves pods in chunks, using the item offset as a continue token.
type pagedReader struct {
	client.Reader
	forcedContinue string
	pods           []corev1.Pod
	lists          int
	lastLimit      int64
}

func (r *pagedReader) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := &client.ListOptions{}
	o.ApplyOptions(opts)

	r.lists++
	r.lastLimit = o.Limit

	start := 0
	if o.Continue != "" {
		var err error
		if start, err = strconv.Atoi(o.Continue); err != nil {
			return err
		}
	}

	end := min(start+int(o.Limit), len(r.pods))

	podList, ok := list.(*corev1.PodList)
	if !ok {
		return errors.New("pagedReader only serves PodList")
	}
	podList.Items = append([]corev1.Pod(nil), r.pods[start:end]...)

	switch {
	case r.forcedContinue != "":
		podList.Continue = r.forcedContinue
	case end < len(r.pods):
		podList.Continue = strconv.Itoa(end)
	}

	return nil
}

func TestEachPage(t *testing.T) {
	pods := func(n int) []corev1.Pod {
		out := make([]corev1.Pod, n)
		for i := range out {
			out[i].Name = "pod-" + strconv.Itoa(i)
		}
		return out
	}

	newList := func() *corev1.PodList { return new(corev1.PodList) }

	t.Run("walks every page", func(t *testing.T) {
		r := &pagedReader{pods: pods(5)}

		var seen []string
		require.NoError(t, EachPage(t.Context(), r, newList, func(page *corev1.PodList) (bool, error) {
			for _, p := range page.Items {
				seen = append(seen, p.Name)
			}
			return true, nil
		}, client.Limit(2)))

		require.Len(t, seen, 5)
		require.Equal(t, 3, r.lists)
	})

	t.Run("defaults the page size", func(t *testing.T) {
		r := &pagedReader{pods: pods(1)}

		require.NoError(t, EachPage(t.Context(), r, newList, func(*corev1.PodList) (bool, error) {
			return true, nil
		}))

		require.Equal(t, int64(DefaultPageSize), r.lastLimit)
	})

	t.Run("stops early", func(t *testing.T) {
		r := &pagedReader{pods: pods(10)}

		require.NoError(t, EachPage(t.Context(), r, newList, func(*corev1.PodList) (bool, error) {
			return false, nil
		}, client.Limit(2)))

		require.Equal(t, 1, r.lists)
	})

	t.Run("propagates the handler error", func(t *testing.T) {
		r := &pagedReader{pods: pods(4)}
		handlerErr := errors.New("boom")

		err := EachPage(t.Context(), r, newList, func(*corev1.PodList) (bool, error) {
			return true, handlerErr
		}, client.Limit(2))

		require.ErrorIs(t, err, handlerErr)
		require.Equal(t, 1, r.lists)
	})

	t.Run("rejects a cached reader", func(t *testing.T) {
		r := &pagedReader{pods: pods(4), forcedContinue: cachedReaderContinue}

		err := EachPage(t.Context(), r, newList, func(*corev1.PodList) (bool, error) {
			return true, nil
		}, client.Limit(2))

		require.ErrorContains(t, err, "uncached reader is required")
		require.Equal(t, 1, r.lists)
	})
}
