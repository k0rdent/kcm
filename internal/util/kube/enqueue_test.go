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
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
)

func Test_isRetryable(t *testing.T) {
	t.Parallel()

	gr := schema.GroupResource{Group: "", Resource: "pods"}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"wrapped context canceled", errors.Join(errors.New("x"), context.Canceled), false},
		{"not found", apierrors.NewNotFound(gr, "p"), false},
		{"gone", &apierrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusGone, Reason: metav1.StatusReasonGone}}, false},
		{"unauthorized", apierrors.NewUnauthorized("u"), false},
		{"forbidden", apierrors.NewForbidden(gr, "p", errors.New("nope")), false},
		{"bad request", apierrors.NewBadRequest("br"), false},
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Kind: "Pod"}, "p", nil), false},
		{"method not supported", apierrors.NewMethodNotSupported(gr, "GET"), false},
		{"request entity too large", apierrors.NewRequestEntityTooLargeError("big"), false},
		{"timeout", apierrors.NewTimeoutError("t", 1), true},
		{"server timeout", apierrors.NewServerTimeout(gr, "list", 1), true},
		{"too many requests", apierrors.NewTooManyRequestsError("rl"), true},
		{"service unavailable", apierrors.NewServiceUnavailable("u"), true},
		{"internal error", apierrors.NewInternalError(errors.New("boom")), true},
		{"plain", errors.New("plain"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isRetryable(tc.err); got != tc.want {
				t.Fatalf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func Test_callWithRetry(t *testing.T) {
	t.Parallel()

	var (
		terminal  = apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "p")
		transient = apierrors.NewServiceUnavailable("temp")
		okReqs    = []ctrl.Request{{}}
	)

	cases := []struct {
		name        string
		backoff     wait.Backoff
		ctx         func(t *testing.T) context.Context
		fn          func(call int) ([]ctrl.Request, error)
		wantCalls   int
		wantErrIs   error
		wantReqsLen int
	}{
		{
			name:      "terminal short-circuits",
			backoff:   fastBackoff(t, 5),
			fn:        func(int) ([]ctrl.Request, error) { return nil, terminal },
			wantCalls: 1,
			wantErrIs: terminal,
		},
		{
			name:    "retries transient until success",
			backoff: fastBackoff(t, 5),
			fn: func(call int) ([]ctrl.Request, error) {
				if call < 3 {
					return nil, transient
				}
				return okReqs, nil
			},
			wantCalls:   3,
			wantReqsLen: len(okReqs),
		},
		{
			name:      "exhausted steps returns last error",
			backoff:   fastBackoff(t, 3),
			fn:        func(int) ([]ctrl.Request, error) { return nil, transient },
			wantCalls: 3,
			wantErrIs: transient,
		},
		{
			name: "pre-cancelled context stops after first attempt",
			// large base so ctx.Done wins the select deterministically; a
			// short delay would race with time.After since both channels are
			// ready and select picks at random
			backoff: wait.Backoff{Duration: time.Hour, Factor: 1, Steps: 5, Cap: time.Hour},
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			fn:        func(int) ([]ctrl.Request, error) { return nil, transient },
			wantCalls: 1,
			wantErrIs: context.Canceled,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(t)
			}

			calls := 0
			reqs, err := callWithRetry(ctx, tc.backoff, func() ([]ctrl.Request, error) {
				calls++
				return tc.fn(calls)
			})

			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}

			if tc.wantErrIs == nil {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			} else if !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.wantErrIs)
			}

			if len(reqs) != tc.wantReqsLen {
				t.Fatalf("len(reqs) = %d, want %d", len(reqs), tc.wantReqsLen)
			}
		})
	}

	t.Run("honors retry-after", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// 429 carrying a 3s Retry-After hint; the backoff base is 1ns so the
			// hint must dominate the sleep
			rateLimited := &apierrors.StatusError{ErrStatus: metav1.Status{
				Status:  metav1.StatusFailure,
				Code:    http.StatusTooManyRequests,
				Reason:  metav1.StatusReasonTooManyRequests,
				Message: "slow down",
				Details: &metav1.StatusDetails{RetryAfterSeconds: 3},
			}}
			backoff := wait.Backoff{Duration: time.Nanosecond, Factor: 1, Steps: 2, Cap: 10 * time.Second}

			calls := 0
			start := time.Now()
			_, err := callWithRetry(t.Context(), backoff, func() ([]ctrl.Request, error) {
				calls++
				return nil, rateLimited
			})
			elapsed := time.Since(start)

			if !errors.Is(err, rateLimited) {
				t.Fatalf("err = %v, want errors.Is(rateLimited)", err)
			}

			if calls != 2 {
				t.Fatalf("calls = %d, want 2", calls)
			}

			if want := 3 * time.Second; elapsed != want {
				t.Fatalf("elapsed = %s, want exactly %s (Retry-After hint must dominate)", elapsed, want)
			}
		})
	})
}

func fastBackoff(t *testing.T, steps int) wait.Backoff {
	t.Helper()
	return wait.Backoff{Duration: time.Nanosecond, Factor: 1, Steps: steps, Cap: time.Millisecond}
}
