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

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

const pollerTestNamespace = "default"

func Test_capiClusterConditionDrifted(t *testing.T) {
	t.Parallel()

	readyConds := getCAPIClusterReadyConditions()

	tests := map[string]struct {
		cd          *kcmv1.ClusterDeployment
		cluster     *clusterapiv1.Cluster
		listErr     error
		wantChanged bool
		wantErrSub  string
	}{
		"no capi cluster, no current condition": {
			cd: newClusterDeployment(t, "a"),
		},
		"no capi cluster, current Deleting -> drift": {
			cd: newClusterDeployment(t, "a", withCondition(metav1.Condition{
				Type:   kcmv1.CAPIClusterSummaryCondition,
				Status: metav1.ConditionFalse,
				Reason: clusterapiv1.DeletingReason,
			})),
			wantChanged: true,
		},
		"no capi cluster, current healthy -> drift (cluster vanished)": {
			cd: newClusterDeployment(t, "a", withCondition(metav1.Condition{
				Type:   kcmv1.CAPIClusterSummaryCondition,
				Status: metav1.ConditionTrue,
				Reason: "InfoReported",
			})),
			wantChanged: true,
		},
		"no capi cluster, current Missing -> no drift (already reflected)": {
			cd: newClusterDeployment(t, "a", withCondition(metav1.Condition{
				Type:   kcmv1.CAPIClusterSummaryCondition,
				Status: metav1.ConditionFalse,
				Reason: kcmv1.CAPIClusterMissingReason,
			})),
		},
		"no capi cluster, current DeletionCompleted -> no drift (already reflected)": {
			cd: newClusterDeployment(t, "a", withCondition(metav1.Condition{
				Type:   kcmv1.CAPIClusterSummaryCondition,
				Status: metav1.ConditionTrue,
				Reason: kcmv1.DeletionCompletedReason,
			})),
		},
		"capi cluster present, no current condition -> drift": {
			cd:          newClusterDeployment(t, "a"),
			cluster:     newCAPICluster(t, "a", readyConds),
			wantChanged: true,
		},
		"capi cluster present, condition matches -> no drift": {
			cd:      newClusterDeployment(t, "a", withMatchingSummaryCondition(t, newCAPICluster(t, "a", readyConds))),
			cluster: newCAPICluster(t, "a", readyConds),
		},
		"capi cluster present, condition message differs -> drift": {
			cd: newClusterDeployment(t, "a", withCondition(metav1.Condition{
				Type:    kcmv1.CAPIClusterSummaryCondition,
				Status:  metav1.ConditionTrue,
				Reason:  "InfoReported",
				Message: "stale message",
			})),
			cluster:     newCAPICluster(t, "a", readyConds),
			wantChanged: true,
		},
		"list error propagates": {
			cd:         newClusterDeployment(t, "a"),
			listErr:    errors.New("boom"),
			wantErrSub: "failed to list CAPI Clusters",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			builder := fake.NewClientBuilder().WithScheme(testscheme.Scheme)
			if tc.cluster != nil {
				builder = builder.WithObjects(tc.cluster)
			}
			if tc.listErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
						return tc.listErr
					},
				})
			}
			cl := builder.Build()

			r := &ClusterDeploymentReconciler{}
			got, err := r.capiClusterConditionDrifted(t.Context(), cl, tc.cd)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", got, tc.wantChanged)
			}
		})
	}
}

func Test_resolveCAPIClusterClient(t *testing.T) {
	t.Parallel()

	type wantClient int
	const (
		wantNone wantClient = iota
		wantMgmt
		wantSentinel
	)

	tests := map[string]struct {
		cred         *kcmv1.Credential
		cacheRegion  string // if non-empty, cache is pre-seeded with sentinel at this region
		want         wantClient
		wantNotFound bool
	}{
		"missing credential bubbles up not-found": {
			want:         wantNone,
			wantNotFound: true,
		},
		"empty region returns mgmt client": {
			cred: newCredential(t, "a", ""),
			want: wantMgmt,
		},
		"cache hit short-circuits regional lookup": {
			cred:        newCredential(t, "a", "region-x"),
			cacheRegion: "region-x",
			want:        wantSentinel,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mgmtBuilder := fake.NewClientBuilder().WithScheme(testscheme.Scheme)
			if tc.cred != nil {
				mgmtBuilder = mgmtBuilder.WithObjects(tc.cred)
			}
			mgmt := mgmtBuilder.Build()
			sentinel := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

			cache := map[string]client.Client{}
			if tc.cacheRegion != "" {
				cache[tc.cacheRegion] = sentinel
			}

			r := &ClusterDeploymentReconciler{MgmtClient: mgmt, SystemNamespace: "kcm-system"}

			got, err := r.resolveCAPIClusterClient(t.Context(), newClusterDeployment(t, "a"), cache)
			if tc.wantNotFound {
				if err == nil {
					t.Fatalf("expected not-found error, got nil")
				}
				if !apierrors.IsNotFound(err) && !apierrors.IsNotFound(errors.Unwrap(err)) {
					t.Fatalf("err = %v, want IsNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch tc.want {
			case wantMgmt:
				if got != mgmt {
					t.Fatalf("got non-mgmt client; want mgmt")
				}
				if len(cache) != 0 {
					t.Fatalf("cache populated for empty region: len=%d", len(cache))
				}
			case wantSentinel:
				if got != sentinel {
					t.Fatalf("got non-sentinel client; want cached sentinel")
				}
			case wantNone:
				t.Fatalf("expectation not set for %q", name)
			}
		})
	}
}

func Test_capiClusterPollEnqueue(t *testing.T) {
	t.Parallel()

	readyConds := getCAPIClusterReadyConditions()

	tests := map[string]struct {
		objects    []client.Object
		listErr    error
		wantNames  []string
		wantErrSub string
	}{
		"empty list returns nothing": {},
		"list error propagates": {
			listErr:    errors.New("boom"),
			wantErrSub: "failed to list ClusterDeployments",
		},
		"deleting CD skipped, drifting enqueued, steady ignored": {
			objects: func() []client.Object {
				steady := newClusterDeployment(
					t, "steady",
					withMatchingSummaryCondition(t, newCAPICluster(t, "steady", readyConds)),
				)
				deleting := newClusterDeployment(t, "deleting", withDeletionTimestamp())
				drifting := newClusterDeployment(t, "drifting")
				return []client.Object{
					deleting, newCredential(t, "deleting", ""),
					drifting, newCredential(t, "drifting", ""),
					steady, newCredential(t, "steady", ""),
					newCAPICluster(t, "drifting", readyConds),
					newCAPICluster(t, "steady", readyConds),
				}
			}(),
			wantNames: []string{"drifting"},
		},
		"dry-run CD skipped": {
			objects: []client.Object{
				newClusterDeployment(t, "dry", withDryRun()),
				newCredential(t, "dry", ""),
			},
			wantNames: nil,
		},
		"CD with previously known but now missing CAPI Cluster is enqueued": {
			objects: []client.Object{
				newClusterDeployment(t, "vanished", withCondition(metav1.Condition{
					Type:   kcmv1.CAPIClusterSummaryCondition,
					Status: metav1.ConditionTrue,
					Reason: "InfoReported",
				})),
				newCredential(t, "vanished", ""),
				// no CAPI Cluster object
			},
			wantNames: []string{"vanished"},
		},
		"CD already reflecting Missing is not re-enqueued": {
			objects: []client.Object{
				newClusterDeployment(t, "missing", withCondition(metav1.Condition{
					Type:   kcmv1.CAPIClusterSummaryCondition,
					Status: metav1.ConditionFalse,
					Reason: kcmv1.CAPIClusterMissingReason,
				})),
				newCredential(t, "missing", ""),
				// no CAPI Cluster object
			},
			wantNames: nil,
		},
		"CD with missing credential is skipped, others still processed": {
			objects: []client.Object{
				newClusterDeployment(t, "orphan"), // no credential -> resolve fails -> skipped
				newClusterDeployment(t, "drifting"),
				newCredential(t, "drifting", ""),
				newCAPICluster(t, "drifting", readyConds),
			},
			wantNames: []string{"drifting"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			builder := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(tc.objects...)
			if tc.listErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
						if _, ok := list.(*kcmv1.ClusterDeploymentList); ok {
							return tc.listErr
						}
						return nil
					},
				})
			}
			mgmt := builder.Build()

			r := &ClusterDeploymentReconciler{MgmtClient: mgmt, SystemNamespace: "kcm-system"}

			got, err := r.capiClusterPollEnqueue(t.Context())
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotNames := enqueuedNames(t, got)
			if !equalUnordered(t, gotNames, tc.wantNames) {
				t.Fatalf("enqueued = %v, want %v", gotNames, tc.wantNames)
			}
		})
	}
}

func newClusterDeployment(t *testing.T, name string, mutators ...func(*kcmv1.ClusterDeployment)) *kcmv1.ClusterDeployment {
	t.Helper()

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pollerTestNamespace,
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			Credential: name + "-cred",
		},
	}
	for _, m := range mutators {
		m(cd)
	}
	return cd
}

func newCredential(t *testing.T, cdName, region string) *kcmv1.Credential {
	t.Helper()

	return &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cdName + "-cred",
			Namespace: pollerTestNamespace,
		},
		Spec: kcmv1.CredentialSpec{Region: region},
	}
}

func newCAPICluster(t *testing.T, name string, conditions []metav1.Condition) *clusterapiv1.Cluster {
	t.Helper()

	return &clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pollerTestNamespace,
			Labels:    map[string]string{kcmv1.FluxHelmChartNameKey: name},
		},
		Status: clusterapiv1.ClusterStatus{Conditions: conditions},
	}
}

func withCondition(c metav1.Condition) func(*kcmv1.ClusterDeployment) {
	return func(cd *kcmv1.ClusterDeployment) {
		apimeta.SetStatusCondition(&cd.Status.Conditions, c)
	}
}

func withDeletionTimestamp() func(*kcmv1.ClusterDeployment) {
	return func(cd *kcmv1.ClusterDeployment) {
		now := metav1.Now()
		cd.DeletionTimestamp = &now
		cd.Finalizers = []string{"kcm.k0rdent.io/test"}
	}
}

func withDryRun() func(*kcmv1.ClusterDeployment) {
	return func(cd *kcmv1.ClusterDeployment) {
		cd.Spec.DryRun = true
	}
}

// withMatchingSummaryCondition seeds the CD with the exact CAPIClusterSummary
// condition the poller would compute for the given CAPI Cluster, so the diff
// comparison reports no drift.
func withMatchingSummaryCondition(t *testing.T, cluster *clusterapiv1.Cluster) func(*kcmv1.ClusterDeployment) {
	t.Helper()

	return func(cd *kcmv1.ClusterDeployment) {
		cond, err := conditionsutil.GetCAPIClusterSummaryCondition(cd, cluster)
		if err != nil {
			t.Fatalf("GetCAPIClusterSummaryCondition: %v", err)
		}
		apimeta.SetStatusCondition(&cd.Status.Conditions, *cond)
	}
}

func enqueuedNames(t *testing.T, cds []*kcmv1.ClusterDeployment) []string {
	t.Helper()

	out := make([]string, len(cds))
	for i, cd := range cds {
		out[i] = cd.Name
	}
	return out
}

func equalUnordered(t *testing.T, a, b []string) bool {
	t.Helper()

	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}
