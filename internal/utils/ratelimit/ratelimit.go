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

package ratelimit

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	DefaultFastDelay       = 5 * time.Second
	DefaultSlowDelay       = 2 * time.Minute
	DefaultMaxFastAttempts = 300
)

// DefaultFastSlow is an untyped no-arg constructor for a workqueue's rate limiter that uses [sigs.k8s.io/controller-runtime/pkg/reconcile.Request] as a type argument.
// It has both overall and per-item rate limiting. The overall is a token bucket and the per-item is fast-slow.
func DefaultFastSlow() workqueue.TypedRateLimiter[ctrl.Request] {
	return TypedFastSlow[ctrl.Request](DefaultFastDelay, DefaultSlowDelay, DefaultMaxFastAttempts)
}

// FastSlow is an untyped constructor for a workqueue's rate limiter that uses [sigs.k8s.io/controller-runtime/pkg/reconcile.Request] as a type argument.
// It has both overall and per-item rate limiting. The overall is a token bucket and the per-item is fast-slow.
func FastSlow(fastDelay, slowDelay time.Duration, maxFastAttempts int) workqueue.TypedRateLimiter[ctrl.Request] {
	return TypedFastSlow[ctrl.Request](fastDelay, slowDelay, maxFastAttempts)
}

// TypedFastSlow is a typed constructor for a workqueue's rate limiter. It has
// both overall and per-item rate limiting. The overall is a token bucket and the per-item is fast-slow.
func TypedFastSlow[T comparable](fastDelay, slowDelay time.Duration, maxFastAttempts int) workqueue.TypedRateLimiter[T] {
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemFastSlowRateLimiter[T](fastDelay, slowDelay, maxFastAttempts),
		// 10 qps, 100 bucket size. This is only for retry speed and its only the overall factor (not per item)
		&workqueue.TypedBucketRateLimiter[T]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
}
