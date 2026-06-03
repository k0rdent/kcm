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
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

// MapFunc maps a watch source object to a set of reconcile requests. Unlike
// the stock [sigs.k8s.io/controller-runtime/pkg/handler.MapFunc] it may report an error to signal that the mapping
// itself failed (e.g. a transient API error while listing dependent objects).
// The wrapping handler retries the call with a bounded exponential backoff
// instead of silently dropping the event.
//
// Returning (nil, nil) is the canonical way to express "no work to enqueue,
// not an error" (for example: the source object did not match an indexer or
// failed a type assertion).
type MapFunc[O client.Object] func(context.Context, O) ([]ctrl.Request, error)

// DefaultBackoff is the bounded exponential backoff applied to [MapFunc] retries.
// It caps the synchronous retry budget so a misbehaving dependency cannot
// indefinitely block the informer dispatch goroutine.
var DefaultBackoff = wait.Backoff{
	Duration: 250 * time.Millisecond,
	Factor:   2.0,
	Jitter:   0.1,
	Steps:    5,
	Cap:      5 * time.Second,
}

// EnqueueRequestsFromMapFunc is a drop-in replacement for
// [sigs.k8s.io/controller-runtime/pkg/handler.EnqueueRequestsFromMapFunc] that retries transient errors raised by
// fn using [DefaultBackoff]. When the backoff is exhausted, the error is
// logged at the dispatch context's logger and the event is dropped, matching
// the prior best-effort semantics.
func EnqueueRequestsFromMapFunc(fn MapFunc[client.Object]) handler.EventHandler {
	return TypedEnqueueRequestsFromMapFunc(fn)
}

// TypedEnqueueRequestsFromMapFunc is the typed variant of
// [EnqueueRequestsFromMapFunc] for use with typed sources (see
// [sigs.k8s.io/controller-runtime/pkg/source.TypedKind]).
func TypedEnqueueRequestsFromMapFunc[O client.Object](fn MapFunc[O]) handler.TypedEventHandler[O, ctrl.Request] {
	return typedEnqueue(fn, DefaultBackoff)
}

func typedEnqueue[O client.Object](fn MapFunc[O], backoff wait.Backoff) handler.TypedEventHandler[O, ctrl.Request] {
	run := func(ctx context.Context, obj O, q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
		reqs, err := callWithRetry(ctx, backoff, func() ([]ctrl.Request, error) {
			return fn(ctx, obj)
		})
		if err != nil {
			// normal shutdown signal
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			ctrl.LoggerFrom(ctx).
				WithName("watch-mapper").
				Error(err, "map function failed after retries; dropping watch event",
					"object", client.ObjectKeyFromObject(obj),
					"gvk", obj.GetObjectKind().GroupVersionKind())
			return
		}

		for _, r := range reqs {
			q.Add(r)
		}
	}

	return handler.TypedFuncs[O, ctrl.Request]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[O], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
			run(ctx, e.Object, q)
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[O], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
			run(ctx, e.ObjectNew, q)
		},
		DeleteFunc: func(ctx context.Context, e event.TypedDeleteEvent[O], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
			run(ctx, e.Object, q)
		},
		GenericFunc: func(ctx context.Context, e event.TypedGenericEvent[O], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
			run(ctx, e.Object, q)
		},
	}
}

// callWithRetry invokes fn with the given bounded exponential backoff, retrying
// while [isRetryable] reports true. When the server attaches a Retry-After
// hint (see [k8s.io/apimachinery/pkg/api/errors.SuggestsClientDelay]), that delay overrides the
// backoff step for the next attempt - never sleeping less than the backoff
// would have, and capped by the backoff Cap to keep the dispatch goroutine
// responsive. The context is honoured between attempts.
func callWithRetry(ctx context.Context, backoff wait.Backoff, fn func() ([]ctrl.Request, error)) ([]ctrl.Request, error) {
	var (
		reqs []ctrl.Request
		err  error
	)
	for {
		reqs, err = fn()
		if !isRetryable(err) {
			return reqs, err
		}
		if backoff.Steps <= 1 {
			return reqs, err
		}

		delay := backoff.Step()
		if hint, ok := apierrors.SuggestsClientDelay(err); ok {
			suggested := time.Duration(hint) * time.Second
			if suggested > delay {
				delay = suggested
			}
			if backoff.Cap > 0 && delay > backoff.Cap {
				delay = backoff.Cap
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// isRetryable reports whether err is worth retrying inside a [MapFunc]. We use
// a deny-list: any error not classified as terminal is treated as transient
// and retried. This matches what most Kubernetes controllers do (including
// kubectl and the in-tree controllers) and avoids silently dropping events
// when the apiserver invents a new error shape.
//
// Terminal categories:
//   - context cancellation / deadline (callers will be redriven by the next watch event);
//   - NotFound / Gone (the dependency does not exist, retrying cannot change that);
//   - auth/RBAC failures (401, 403) - only a human can fix them;
//   - client-side request bugs (400, 422, 405, 406, 415, 413) - retrying sends the same broken request.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case apierrors.IsNotFound(err), apierrors.IsGone(err):
		return false
	case apierrors.IsUnauthorized(err), apierrors.IsForbidden(err):
		return false
	case apierrors.IsBadRequest(err), apierrors.IsInvalid(err):
		return false
	case apierrors.IsMethodNotSupported(err),
		apierrors.IsNotAcceptable(err),
		apierrors.IsUnsupportedMediaType(err),
		apierrors.IsRequestEntityTooLargeError(err):
		return false
	}
	return true
}
