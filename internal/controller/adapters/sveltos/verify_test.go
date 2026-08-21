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

package sveltos

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// testRulesYAML is the same rule body shipped by the chart at
// templates/provider/kcm/templates/health-rules/default-rules.yaml under
// data.rules. Duplicated here so the test suite is self-contained and
// covers the actual default-rule CEL logic.
const testRulesYAML = `
- group: apps
  version: v1
  kind: Deployment
  scope: Namespaced
  healthy: |
    has(obj.status.observedGeneration) &&
    int(obj.status.observedGeneration) >= int(obj.metadata.generation) &&
    int(has(obj.spec.replicas) ? obj.spec.replicas : 1) ==
      int(has(obj.status.readyReplicas) ? obj.status.readyReplicas : 0) &&
    int(has(obj.spec.replicas) ? obj.spec.replicas : 1) ==
      int(has(obj.status.updatedReplicas) ? obj.status.updatedReplicas : 0)
  message: |
    !has(obj.status.observedGeneration) ||
    int(obj.status.observedGeneration) < int(obj.metadata.generation)
      ? "deployment controller has not observed latest spec"
      : string(int(has(obj.status.readyReplicas) ? obj.status.readyReplicas : 0)) + "/" +
        string(int(has(obj.spec.replicas) ? obj.spec.replicas : 1)) + " replicas ready, " +
        string(int(has(obj.status.updatedReplicas) ? obj.status.updatedReplicas : 0)) + " updated"
- group: apps
  version: v1
  kind: StatefulSet
  scope: Namespaced
  healthy: |
    has(obj.status.observedGeneration) &&
    int(obj.status.observedGeneration) >= int(obj.metadata.generation) &&
    int(has(obj.spec.replicas) ? obj.spec.replicas : 1) ==
      int(has(obj.status.readyReplicas) ? obj.status.readyReplicas : 0) &&
    int(has(obj.spec.replicas) ? obj.spec.replicas : 1) ==
      int(has(obj.status.updatedReplicas) ? obj.status.updatedReplicas : 0) &&
    has(obj.status.currentRevision) && has(obj.status.updateRevision) &&
    obj.status.currentRevision == obj.status.updateRevision
  message: |
    has(obj.status.currentRevision) && has(obj.status.updateRevision) &&
    obj.status.currentRevision != obj.status.updateRevision
      ? "rolling update in progress (currentRevision != updateRevision)"
      : string(int(has(obj.status.readyReplicas) ? obj.status.readyReplicas : 0)) + "/" +
        string(int(has(obj.spec.replicas) ? obj.spec.replicas : 1)) + " replicas ready"
- group: apps
  version: v1
  kind: DaemonSet
  scope: Namespaced
  healthy: |
    has(obj.status.observedGeneration) &&
    int(obj.status.observedGeneration) >= int(obj.metadata.generation) &&
    has(obj.status.desiredNumberScheduled) &&
    has(obj.status.numberReady) &&
    has(obj.status.updatedNumberScheduled) &&
    int(obj.status.desiredNumberScheduled) == int(obj.status.numberReady) &&
    int(obj.status.desiredNumberScheduled) == int(obj.status.updatedNumberScheduled)
  message: |
    string(int(has(obj.status.numberReady) ? obj.status.numberReady : 0)) + "/" +
    string(int(has(obj.status.desiredNumberScheduled) ? obj.status.desiredNumberScheduled : 0)) +
    " nodes ready"
- group: ""
  version: v1
  kind: Pod
  scope: Namespaced
  healthy: |
    (has(obj.status.phase) && obj.status.phase == "Succeeded") ||
    (has(obj.metadata) && has(obj.metadata.ownerReferences) &&
     obj.metadata.ownerReferences.exists(o, has(o.kind) && o.kind == "Job") &&
     has(obj.status.phase) && obj.status.phase == "Failed") ||
    (has(obj.status.conditions) &&
     obj.status.conditions.exists(c, c.type == "Ready" && c.status == "True"))
  message: |
    has(obj.status.containerStatuses)
      ? obj.status.containerStatuses
          .filter(c, !c.ready)
          .map(c, c.name + ": " +
            (has(c.state.waiting) && has(c.state.waiting.reason) ? c.state.waiting.reason :
             (has(c.state.terminated) && has(c.state.terminated.reason) ? c.state.terminated.reason :
              "not ready")))
          .join(", ")
      : "pod not ready"
- group: ""
  version: v1
  kind: PersistentVolumeClaim
  scope: Namespaced
  healthy: |
    has(obj.status.phase) && obj.status.phase == "Bound"
  message: |
    has(obj.status.phase) ? "phase=" + obj.status.phase : "no phase reported"
- group: ""
  version: v1
  kind: PersistentVolume
  scope: Cluster
  healthy: |
    has(obj.status.phase) && obj.status.phase == "Bound"
  message: |
    has(obj.status.phase) ? "phase=" + obj.status.phase : "no phase reported"
`

const (
	dummyDeployment = "web"
)

// testRules is the compiled ruleSet built from testRulesYAML. Construction
// failure is a programming error in the test fixture — panic so the suite
// fails loudly rather than silently running against a partial rule set.
var testRules = func() ruleSet {
	docs := parseRuleYAMLOrPanic(testRulesYAML, "test")
	rules, errs := rulesFromDocs(docs)
	if len(errs) > 0 {
		panic(fmt.Errorf("test rules failed to compile: %v", errs))
	}
	rs := make(ruleSet)
	for _, r := range rules {
		gk := r.GVK.GroupKind()
		rs[gk] = append(rs[gk], r)
	}
	return rs
}()

func parseRuleYAMLOrPanic(yamlBody, sourcePrefix string) []sourcedRuleDoc {
	var docs []ruleDoc
	if err := yaml.Unmarshal([]byte(yamlBody), &docs); err != nil {
		panic(fmt.Errorf("parse test rules yaml: %w", err))
	}
	sourced := make([]sourcedRuleDoc, len(docs))
	for i, d := range docs {
		sourced[i] = sourcedRuleDoc{Doc: d, Source: fmt.Sprintf("%s#%d", sourcePrefix, i)}
	}
	return sourced
}

// TestTestRules_AllPresent asserts the embedded test rule set has one entry
// for each expected GroupKind. Acts as a sanity check on the parser as
// well as the rule set's contents.
func TestTestRules_AllPresent(t *testing.T) {
	want := []schema.GroupKind{
		{Group: "apps", Kind: "Deployment"},
		{Group: "apps", Kind: "StatefulSet"},
		{Group: "apps", Kind: "DaemonSet"},
		{Group: "", Kind: "Pod"},
		{Group: "", Kind: "PersistentVolumeClaim"},
		{Group: "", Kind: "PersistentVolume"},
	}
	for _, gk := range want {
		rules, ok := testRules[gk]
		assert.True(t, ok, "missing rule for %s", gk)
		assert.Len(t, rules, 1, "expected exactly one default rule for %s", gk)
	}
	assert.Len(t, testRules, len(want), "extra rules present")
}

// evalRule looks up a single rule by group/kind from testRules and
// evaluates it. Used by per-rule tests that don't care about layering.
func evalRule(t *testing.T, group, kind string, payload map[string]any) (bool, string) {
	t.Helper()
	rules, ok := testRules[schema.GroupKind{Group: group, Kind: kind}]
	require.True(t, ok, "rule for %s/%s not loaded", group, kind)
	require.Len(t, rules, 1, "evalRule expects a single rule per GroupKind in testRules")
	rule := rules[0]
	u := &unstructured.Unstructured{Object: payload}
	u.SetGroupVersionKind(rule.GVK)
	healthy, reason, err := rule.evaluate(objActivation{obj: u.Object})
	require.NoError(t, err)
	return healthy, reason
}

func TestRule_Deployment(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]any
		wantHealthy bool
	}{
		{
			name: "all replicas ready and updated",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(2)},
				"spec":     map[string]any{"replicas": int64(3)},
				"status": map[string]any{
					"observedGeneration": int64(2),
					"readyReplicas":      int64(3),
					"updatedReplicas":    int64(3),
				},
			},
			wantHealthy: true,
		},
		{
			name: "generation lag",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(3)},
				"spec":     map[string]any{"replicas": int64(3)},
				"status": map[string]any{
					"observedGeneration": int64(2),
					"readyReplicas":      int64(3),
					"updatedReplicas":    int64(3),
				},
			},
			wantHealthy: false,
		},
		{
			name: "not enough ready",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(1)},
				"spec":     map[string]any{"replicas": int64(3)},
				"status": map[string]any{
					"observedGeneration": int64(1),
					"readyReplicas":      int64(2),
					"updatedReplicas":    int64(3),
				},
			},
			wantHealthy: false,
		},
		{
			name: "rollout in progress",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(1)},
				"spec":     map[string]any{"replicas": int64(3)},
				"status": map[string]any{
					"observedGeneration": int64(1),
					"readyReplicas":      int64(3),
					"updatedReplicas":    int64(2),
				},
			},
			wantHealthy: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthy, _ := evalRule(t, "apps", "Deployment", tt.payload)
			assert.Equal(t, tt.wantHealthy, healthy)
		})
	}
}

func TestRule_StatefulSet(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]any
		wantHealthy bool
	}{
		{
			name: "healthy",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(1)},
				"spec":     map[string]any{"replicas": int64(2)},
				"status": map[string]any{
					"observedGeneration": int64(1),
					"readyReplicas":      int64(2),
					"updatedReplicas":    int64(2),
					"currentRevision":    "sts-abc",
					"updateRevision":     "sts-abc",
				},
			},
			wantHealthy: true,
		},
		{
			name: "revision drift",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(1)},
				"spec":     map[string]any{"replicas": int64(2)},
				"status": map[string]any{
					"observedGeneration": int64(1),
					"readyReplicas":      int64(2),
					"updatedReplicas":    int64(2),
					"currentRevision":    "sts-abc",
					"updateRevision":     "sts-xyz",
				},
			},
			wantHealthy: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthy, _ := evalRule(t, "apps", "StatefulSet", tt.payload)
			assert.Equal(t, tt.wantHealthy, healthy)
		})
	}
}

func TestRule_DaemonSet(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]any
		wantHealthy bool
	}{
		{
			name: "healthy",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(1)},
				"status": map[string]any{
					"observedGeneration":     int64(1),
					"desiredNumberScheduled": int64(3),
					"numberReady":            int64(3),
					"updatedNumberScheduled": int64(3),
				},
			},
			wantHealthy: true,
		},
		{
			name: "not all nodes ready",
			payload: map[string]any{
				"metadata": map[string]any{"generation": int64(1)},
				"status": map[string]any{
					"observedGeneration":     int64(1),
					"desiredNumberScheduled": int64(3),
					"numberReady":            int64(2),
					"updatedNumberScheduled": int64(3),
				},
			},
			wantHealthy: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthy, _ := evalRule(t, "apps", "DaemonSet", tt.payload)
			assert.Equal(t, tt.wantHealthy, healthy)
		})
	}
}

func TestRule_Pod(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]any
		wantHealthy bool
	}{
		{
			name: "ready",
			payload: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			},
			wantHealthy: true,
		},
		{
			name: "not ready",
			payload: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False"},
					},
				},
			},
			wantHealthy: false,
		},
		{
			name: "no conditions",
			payload: map[string]any{
				"status": map[string]any{},
			},
			wantHealthy: false,
		},
		// A pod that ran to completion carries Ready=False with
		// reason=PodCompleted permanently, so the Ready assertion alone
		// could never pass for the setup/hook Jobs charts ship
		// (DEVINT-655). Terminal success is healthy regardless of owner:
		// a bare run-to-completion pod is as done as a Job's.
		{
			name: "succeeded job-owned pod",
			payload: map[string]any{
				"metadata": map[string]any{
					"ownerReferences": []any{
						map[string]any{"kind": "Job", "name": "temporal-schema"},
					},
				},
				"status": map[string]any{
					"phase": "Succeeded",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False", "reason": "PodCompleted"},
					},
				},
			},
			wantHealthy: true,
		},
		{
			name: "succeeded pod with no owner",
			payload: map[string]any{
				"status": map[string]any{
					"phase": "Succeeded",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False", "reason": "PodCompleted"},
					},
				},
			},
			wantHealthy: true,
		},
		// The Job controller owns retries and the verifier holds no
		// batch/jobs RBAC to ask whether a later attempt succeeded, so a
		// failed attempt left behind by a Job must not block readiness.
		{
			name: "failed job-owned pod",
			payload: map[string]any{
				"metadata": map[string]any{
					"ownerReferences": []any{
						map[string]any{"kind": "Job", "name": "temporal-schema"},
					},
				},
				"status": map[string]any{
					"phase": "Failed",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False"},
					},
				},
			},
			wantHealthy: true,
		},
		// Nothing but a Job gets the terminal-failure exemption.
		{
			name: "failed pod owned by non-job",
			payload: map[string]any{
				"metadata": map[string]any{
					"ownerReferences": []any{
						map[string]any{"kind": "ReplicaSet", "name": "web-abc123"},
					},
				},
				"status": map[string]any{
					"phase": "Failed",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False"},
					},
				},
			},
			wantHealthy: false,
		},
		{
			name: "failed pod with no owner",
			payload: map[string]any{
				"status": map[string]any{
					"phase": "Failed",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False"},
					},
				},
			},
			wantHealthy: false,
		},
		// Guards against over-broadening: a Job pod still running but
		// not yet ready must stay unhealthy even though it now passes
		// through two phase clauses before reaching the Ready check.
		{
			name: "running job-owned pod not ready",
			payload: map[string]any{
				"metadata": map[string]any{
					"ownerReferences": []any{
						map[string]any{"kind": "Job", "name": "temporal-schema"},
					},
				},
				"status": map[string]any{
					"phase": "Running",
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "False"},
					},
				},
			},
			wantHealthy: false,
		},
		// ownerReferences entries always carry kind in practice, but a
		// malformed entry must not error the evaluation — evalRule
		// require.NoError's, and in production an eval error surfaces as
		// a reconcile error instead of a verdict.
		{
			name: "failed pod with owner missing kind",
			payload: map[string]any{
				"metadata": map[string]any{
					"ownerReferences": []any{
						map[string]any{"name": "mystery"},
					},
				},
				"status": map[string]any{
					"phase": "Failed",
				},
			},
			wantHealthy: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthy, _ := evalRule(t, "", "Pod", tt.payload)
			assert.Equal(t, tt.wantHealthy, healthy)
		})
	}
}

func TestRule_PVC(t *testing.T) {
	tests := []struct {
		name        string
		phase       string
		wantHealthy bool
	}{
		{name: "bound", phase: "Bound", wantHealthy: true},
		{name: "pending", phase: "Pending", wantHealthy: false},
		{name: "lost", phase: "Lost", wantHealthy: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthy, _ := evalRule(t, "", "PersistentVolumeClaim", map[string]any{
				"status": map[string]any{"phase": tt.phase},
			})
			assert.Equal(t, tt.wantHealthy, healthy)
		})
	}
}

func TestRule_PV(t *testing.T) {
	healthy, _ := evalRule(t, "", "PersistentVolume", map[string]any{
		"status": map[string]any{"phase": "Bound"},
	})
	assert.True(t, healthy)
	healthy, _ = evalRule(t, "", "PersistentVolume", map[string]any{
		"status": map[string]any{"phase": "Available"},
	})
	assert.False(t, healthy)
}

// --- verifyHelmServiceOnCluster ---

const (
	releaseName = "myapp"
	releaseNs   = "myapp-ns"
)

func newFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))

	// Seed a RESTMapper so resolveServedVersion sees the same GVKs the
	// fake client stores. The default fake-client mapper is empty and
	// would silent-skip every GroupKind the verifier queries.
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		appsv1.SchemeGroupVersion,
		corev1.SchemeGroupVersion,
	})
	mapper.Add(appsv1.SchemeGroupVersion.WithKind("Deployment"), meta.RESTScopeNamespace)
	mapper.Add(appsv1.SchemeGroupVersion.WithKind("StatefulSet"), meta.RESTScopeNamespace)
	mapper.Add(appsv1.SchemeGroupVersion.WithKind("DaemonSet"), meta.RESTScopeNamespace)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("Pod"), meta.RESTScopeNamespace)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("PersistentVolumeClaim"), meta.RESTScopeNamespace)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("PersistentVolume"), meta.RESTScopeRoot)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithRESTMapper(mapper).
		WithObjects(objects...).
		Build()
}

func makeHealthyDeployment(name string) *appsv1.Deployment {
	var three int32 = 3
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  releaseNs,
			Generation: 1,
			Labels:     map[string]string{"release": releaseName},
		},
		Spec: appsv1.DeploymentSpec{Replicas: &three},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           3,
			ReadyReplicas:      3,
			UpdatedReplicas:    3,
		},
	}
}

func makeUnhealthyDeployment() *appsv1.Deployment {
	d := makeHealthyDeployment(dummyDeployment)
	d.Status.ReadyReplicas = 1
	return d
}

func makeReadyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: releaseNs,
			Labels:    map[string]string{"release": releaseName},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func makeUnreadyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: releaseNs,
			Labels:    map[string]string{"release": releaseName},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}
}

// makeJobPod builds a pod owned by a Job in a terminal phase, shaped the
// way the kubelet leaves one behind: Ready=False with reason=PodCompleted
// on success. Callers pass corev1.PodSucceeded or corev1.PodFailed.
func makeJobPod(name string, phase corev1.PodPhase) *corev1.Pod {
	reason := ""
	if phase == corev1.PodSucceeded {
		reason = "PodCompleted"
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: releaseNs,
			Labels:    map[string]string{"release": releaseName},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "Job",
				Name:       "temporal-schema",
			}},
		},
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse, Reason: reason},
			},
		},
	}
}

// TestVerifyHelmServiceOnCluster_NoResources_KeepsDeployed asserts that
// an empty verdict set (no labeled resources on the target cluster)
// returns Deployed rather than Failed. Rationale in the function's
// doc-comment: without sveltos release-inventory, we can't distinguish
// a chart deploying only unmonitored Kinds from a chart whose monitored
// Kinds are missing, so we defer to sveltos's Deployed verdict.
func TestVerifyHelmServiceOnCluster_NoResources_KeepsDeployed(t *testing.T) {
	c := newFakeClient(t)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateDeployed, state)
	assert.Empty(t, conds)
}

func TestVerifyHelmServiceOnCluster_AllHealthy(t *testing.T) {
	c := newFakeClient(t,
		makeHealthyDeployment("web"),
		makeReadyPod("web-0"),
	)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateDeployed, state)
	assert.Empty(t, conds)
}

func TestVerifyHelmServiceOnCluster_DeploymentUnhealthy(t *testing.T) {
	c := newFakeClient(t, makeUnhealthyDeployment())
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state)
	require.Len(t, conds, 1)
	assert.Equal(t, "ServiceHealthDeployment", conds[0].Type)
	assert.Contains(t, conds[0].Message, "web")
	assert.Contains(t, conds[0].Message, "rule test#0", "condition message must cite the failing rule's source")
}

func TestVerifyHelmServiceOnCluster_TerminatingPodNotCounted(t *testing.T) {
	terminating := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "web-old",
			Namespace:         releaseNs,
			Labels:            map[string]string{"release": releaseName},
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
			Finalizers:        []string{"kubernetes"},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}

	c := newFakeClient(t, makeHealthyDeployment("web"), terminating)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateDeployed, state, "terminating pod must not pull state to Provisioning")
	assert.Empty(t, conds)
}

// TestVerifyHelmServiceOnCluster_CompletedJobPodNotCounted covers
// DEVINT-655: a chart shipping a one-shot setup Job (Temporal's schema
// and namespace Jobs, the iam-api idp-hook, CNPG bootstrap, …) leaves
// Succeeded pods behind that carry the release's identity labels, so the
// verifier discovers them alongside the real workloads. Kubernetes keeps
// Ready=False/PodCompleted on them for good, and ttlSecondsAfterFinished
// defaults keep them around for hours, so before the terminal-phase
// clauses the whole ServiceSet sat in Provisioning until GC swept them.
func TestVerifyHelmServiceOnCluster_CompletedJobPodNotCounted(t *testing.T) {
	c := newFakeClient(t,
		makeHealthyDeployment("web"),
		makeReadyPod("web-0"),
		makeJobPod("temporal-schema-abc12", corev1.PodSucceeded),
		makeJobPod("temporal-namespace-def34", corev1.PodSucceeded),
		// A flaked first attempt retried by the Job controller.
		makeJobPod("temporal-schema-xyz99", corev1.PodFailed),
	)

	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateDeployed, state, "terminal Job pods must not pull state to Provisioning")
	assert.Empty(t, conds)
}

// TestVerifyHelmServiceOnCluster_FailedNonJobPodStillCounted is the
// negative half of the above: the exemption is scoped to Job-owned pods,
// so a terminally failed pod under any other controller still degrades
// the service.
func TestVerifyHelmServiceOnCluster_FailedNonJobPodStillCounted(t *testing.T) {
	pod := makeJobPod("web-1", corev1.PodFailed)
	pod.OwnerReferences[0].Kind = "ReplicaSet"

	c := newFakeClient(t, makeHealthyDeployment("web"), pod)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state)
	require.Len(t, conds, 1)
	assert.Contains(t, conds[0].Message, "web-1")
}

// TestVerifyHelmServiceOnCluster_TerminatingDeploymentNotCounted asserts
// the generalized "any Kind with deletionTimestamp is ignored" semantic
// — the previous narrow "only Pod" scope was under-generalized. A
// terminating Deployment whose replicas would otherwise fail the CEL
// rule (updatedReplicas < replicas) must be treated as healthy because
// the user asked for it to go away; the CEL rule failing is expected
// behavior during graceful shutdown.
func TestVerifyHelmServiceOnCluster_TerminatingDeploymentNotCounted(t *testing.T) {
	// makeUnhealthyDeployment produces a Deployment with ReadyReplicas=1
	// against Replicas=3 — the CEL rule would classify this as unhealthy
	// under normal circumstances. Adding a deletionTimestamp reclassifies
	// it as user-initiated shutdown and must be ignored.
	terminating := makeUnhealthyDeployment()
	terminating.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	terminating.Finalizers = []string{"foreground-cascading-deletion"}

	c := newFakeClient(t, terminating)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateDeployed, state,
		"terminating Deployment must not pull state to Provisioning even with failing replica counts")
	assert.Empty(t, conds)
}

// TestVerifyHelmServiceOnCluster_OtherReleaseIgnored asserts that
// resources labeled for a DIFFERENT release do not contribute to our
// verdicts. Under the empty-verdicts-echoes-Deployed semantic (see
// verifyHelmServiceOnCluster doc-comment), no matching resources for
// OUR service means we defer to sveltos's Deployed. This is more
// correct than the previous Failed downgrade — a chart labeled for a
// different release genuinely isn't relevant to this service.
func TestVerifyHelmServiceOnCluster_OtherReleaseIgnored(t *testing.T) {
	other := makeHealthyDeployment("other")
	other.Labels["release"] = "different"

	c := newFakeClient(t, other)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateDeployed, state)
	assert.Empty(t, conds)
}

func TestVerifyHelmServiceOnCluster_DiscoversByAppKubernetesIoInstance(t *testing.T) {
	d := makeUnhealthyDeployment()
	delete(d.Labels, "release")
	d.Labels["app.kubernetes.io/instance"] = releaseName

	c := newFakeClient(t, d)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state, "object labeled via app.kubernetes.io/instance must be evaluated")
	require.Len(t, conds, 1)
	assert.Equal(t, "ServiceHealthDeployment", conds[0].Type)
}

func TestVerifyHelmServiceOnCluster_DedupesAcrossLabels(t *testing.T) {
	d := makeUnhealthyDeployment()
	d.Labels["app"] = releaseName
	d.Labels["app.kubernetes.io/instance"] = releaseName
	d.Labels["app.kubernetes.io/name"] = releaseName

	c := newFakeClient(t, d)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state)
	require.Len(t, conds, 1, "duplicate labels must not produce duplicate conditions")
}

// TestVerifyHelmServiceOnCluster_MultiRule_AllMustPass asserts the additive
// semantics: when two rules target the same GroupKind, both must pass for
// the resource to be healthy. The second rule below intentionally always
// reports unhealthy ("false"), so even a perfectly-healthy Deployment
// must be downgraded.
func TestVerifyHelmServiceOnCluster_MultiRule_AllMustPass(t *testing.T) {
	// Build a rule set with the default deployment rule + an extra rule
	// that always fails.
	docs := parseRuleYAMLOrPanic(testRulesYAML, "test")
	docs = append(docs, sourcedRuleDoc{
		Doc: ruleDoc{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
			Scope:   scopeNamespaced,
			Healthy: `false`,
			Message: `"always unhealthy"`,
		},
		Source: "test-extra#0",
	})
	rules, errs := rulesFromDocs(docs)
	require.Empty(t, errs)
	rs := make(ruleSet)
	for _, r := range rules {
		gk := r.GVK.GroupKind()
		rs[gk] = append(rs[gk], r)
	}
	require.Len(t, rs[schema.GroupKind{Group: "apps", Kind: "Deployment"}], 2)

	c := newFakeClient(t, makeHealthyDeployment("web"))
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, rs, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state, "any unhealthy rule must downgrade")
	require.Len(t, conds, 1)
	assert.Contains(t, conds[0].Message, "always unhealthy")
	assert.Contains(t, conds[0].Message, "test-extra#0", "failing rule's source must appear in the condition")
}

// TestCollectVerdicts_ScopeMismatchEmitsSyntheticVerdict asserts that a
// rule declaring the wrong scope (e.g. "Cluster" for a Namespaced Kind
// like Deployment) is REJECTED for its whole GroupKind — no
// cross-namespace List runs — and instead a synthetic unhealthy verdict
// is emitted so the downstream aggregation surfaces the misconfiguration
// on the ServiceSet as a ServiceHealth<Kind> condition. The reviewer-
// motivated safeguard: without this, a Deployment rule mis-marked
// Cluster would List across every namespace and pull in unrelated
// releases.
func TestCollectVerdicts_ScopeMismatchEmitsSyntheticVerdict(t *testing.T) {
	// Rule that ADMITS Cluster scope for apps/Deployment — which is
	// actually Namespaced on the target cluster (per the RESTMapper
	// seeded in newFakeClient).
	docs := []sourcedRuleDoc{{
		Doc: ruleDoc{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
			Scope:   scopeCluster,
			Healthy: `true`,
			Message: `""`,
		},
		Source: "scope-mismatch-test#0",
	}}
	rules, compileErrs := rulesFromDocs(docs)
	require.Empty(t, compileErrs)
	rs := make(ruleSet)
	for _, r := range rules {
		rs[r.GVK.GroupKind()] = append(rs[r.GVK.GroupKind()], r)
	}

	// Seed the fake with an unhealthy Deployment; if the mis-scoped rule
	// were accepted, we'd get a real per-object verdict. Under the fix,
	// the whole GroupKind is skipped and the only verdict is the
	// synthetic HealthRuleScopeMismatch one. err stays nil — the
	// mismatch is a config problem surfaced via the verdict, not
	// propagated as a reconciler error.
	c := newFakeClient(t, makeUnhealthyDeployment())
	verdicts, err := collectVerdicts(context.Background(), c, rs, releaseName, releaseNs)
	require.NoError(t, err, "scope mismatch is not a reconciler error")
	require.Len(t, verdicts, 1, "one synthetic verdict per mis-scoped rule")

	v := verdicts[0]
	assert.False(t, v.Healthy, "synthetic verdict must be unhealthy so the aggregation surfaces it")
	assert.Equal(t, "HealthRuleScopeMismatch", v.Reason)
	assert.Equal(t, "scope-mismatch-test#0", v.RuleSrc,
		"RuleSrc must name the offending rule so operators can locate it")
	assert.Equal(t, "Deployment", v.GVK.Kind, "GVK.Kind identifies the affected Kind on the ServiceSet condition")
	assert.Empty(t, v.Name, "synthetic verdict speaks about the rule, not any specific object")
	assert.Empty(t, v.Namespace)
}

// TestVerifyHelmServiceOnCluster_AggregatesByKind asserts that multiple
// unhealthy resources of the SAME Kind collapse into a single condition
// with a listType=map-safe Type. Two ServiceHealthPod entries would
// otherwise fail the ServiceSet status-writeback schema validation
// (Conditions is +listType=map +listMapKey=type).
func TestVerifyHelmServiceOnCluster_AggregatesByKind(t *testing.T) {
	c := newFakeClient(t,
		makeUnreadyPod("web-a"),
		makeUnreadyPod("web-b"),
		makeUnreadyPod("web-c"),
	)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state)
	require.Len(t, conds, 1, "3 unhealthy Pods must collapse to 1 ServiceHealthPod condition")
	assert.Equal(t, "ServiceHealthPod", conds[0].Type)
	assert.Contains(t, conds[0].Message, "3 Pod unhealthy")
	// All three refs shown because count <= maxUnhealthyRefsPerCondition.
	assert.Contains(t, conds[0].Message, "web-a")
	assert.Contains(t, conds[0].Message, "web-b")
	assert.Contains(t, conds[0].Message, "web-c")
	assert.NotContains(t, conds[0].Message, "…and", "no truncation expected at count=3")
}

// TestVerifyHelmServiceOnCluster_AggregatesByKindAcrossKinds asserts that
// different Kinds produce distinct conditions (one per Kind) — the
// listMapKey uniqueness is per-Type, not per-Kind-per-resource.
func TestVerifyHelmServiceOnCluster_AggregatesByKindAcrossKinds(t *testing.T) {
	c := newFakeClient(t,
		makeUnhealthyDeployment(),
		makeUnreadyPod("web-a"),
		makeUnreadyPod("web-b"),
	)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state)
	require.Len(t, conds, 2, "one condition per Kind")
	condTypes := []string{conds[0].Type, conds[1].Type}
	assert.Contains(t, condTypes, "ServiceHealthDeployment")
	assert.Contains(t, condTypes, "ServiceHealthPod")
}

// TestVerifyHelmServiceOnCluster_TruncatesLargeRefLists asserts that a
// condition message caps ref enumeration at maxUnhealthyRefsPerCondition
// and appends an "…and N more" summary. Sorting by (namespace, name) is
// verified transitively: alphabetically-first refs must appear in the
// message, later ones must not.
func TestVerifyHelmServiceOnCluster_TruncatesLargeRefLists(t *testing.T) {
	// 5 unhealthy pods, cap is 3 → 3 shown, 2 elided.
	c := newFakeClient(t,
		makeUnreadyPod("pod-a"),
		makeUnreadyPod("pod-b"),
		makeUnreadyPod("pod-c"),
		makeUnreadyPod("pod-d"),
		makeUnreadyPod("pod-e"),
	)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state)
	require.Len(t, conds, 1)
	assert.Contains(t, conds[0].Message, "5 Pod unhealthy")
	assert.Contains(t, conds[0].Message, "pod-a")
	assert.Contains(t, conds[0].Message, "pod-b")
	assert.Contains(t, conds[0].Message, "pod-c")
	assert.NotContains(t, conds[0].Message, "pod-d", "refs past cap must be elided")
	assert.NotContains(t, conds[0].Message, "pod-e", "refs past cap must be elided")
	assert.Contains(t, conds[0].Message, "…and 2 more")
}

// --- ruleCache eviction ---

// TestSweepRuleCache_EvictsStaleEntries asserts that sweepRuleCache
// drops entries whose lastAccess is older than ruleCacheTTL while
// preserving fresh ones. This is the leak-prevention hatch from PR #2891
// review — an entry whose owning ConfigMap has been deleted has no way
// to be invalidated by the ResourceVersion check (there's nothing to
// re-read), so TTL-based sweeping is the only mechanism keeping the
// cache bounded on long-lived controllers.
func TestSweepRuleCache_EvictsStaleEntries(t *testing.T) {
	// Isolate this test from other tests that may have populated the
	// process-global cache. Snapshot on entry, restore on exit.
	ruleCache.Lock()
	original := make(map[client.ObjectKey]ruleCacheEntry, len(ruleCache.entries))
	maps.Copy(original, ruleCache.entries)
	ruleCache.entries = make(map[client.ObjectKey]ruleCacheEntry)
	ruleCache.Unlock()
	t.Cleanup(func() {
		ruleCache.Lock()
		ruleCache.entries = original
		ruleCache.Unlock()
	})

	freshKey := client.ObjectKey{Namespace: "ns-a", Name: "fresh"}
	staleKey := client.ObjectKey{Namespace: "ns-b", Name: "stale"}
	now := time.Now()

	ruleCache.Lock()
	ruleCache.entries[freshKey] = ruleCacheEntry{
		resourceVersion: "1",
		lastAccess:      now.Add(-ruleCacheTTL / 2), // half-TTL old, keep
	}
	ruleCache.entries[staleKey] = ruleCacheEntry{
		resourceVersion: "1",
		lastAccess:      now.Add(-2 * ruleCacheTTL), // 2×TTL old, evict
	}
	ruleCache.Unlock()

	sweepRuleCache()

	ruleCache.Lock()
	defer ruleCache.Unlock()
	_, freshOK := ruleCache.entries[freshKey]
	_, staleOK := ruleCache.entries[staleKey]
	assert.True(t, freshOK, "fresh entry must survive sweep")
	assert.False(t, staleOK, "stale entry must be evicted")
}

// TestRulesFromConfigMap_RefreshesLastAccessOnHit asserts that reading a
// cached entry refreshes its lastAccess timestamp. Without this, a hot CM
// whose ResourceVersion never changes would age out and be re-parsed +
// re-compiled unnecessarily.
func TestRulesFromConfigMap_RefreshesLastAccessOnHit(t *testing.T) {
	// Snapshot + isolate.
	ruleCache.Lock()
	original := make(map[client.ObjectKey]ruleCacheEntry, len(ruleCache.entries))
	maps.Copy(original, ruleCache.entries)
	ruleCache.entries = make(map[client.ObjectKey]ruleCacheEntry)
	ruleCache.Unlock()
	t.Cleanup(func() {
		ruleCache.Lock()
		ruleCache.entries = original
		ruleCache.Unlock()
	})

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns-hit",
			Name:            "hot-cm",
			ResourceVersion: "42",
		},
		Data: map[string]string{healthRuleConfigMapDataKey: testRulesYAML},
	}

	// First call populates the cache. Force lastAccess into the past so
	// we can see whether the second call refreshes it.
	_, _ = rulesFromConfigMap(cm)
	key := client.ObjectKey{Namespace: cm.Namespace, Name: cm.Name}

	ruleCache.Lock()
	stalePast := time.Now().Add(-ruleCacheTTL)
	entry := ruleCache.entries[key]
	entry.lastAccess = stalePast
	ruleCache.entries[key] = entry
	ruleCache.Unlock()

	// Second call is a cache hit (same RV) and must refresh lastAccess.
	_, _ = rulesFromConfigMap(cm)

	ruleCache.Lock()
	defer ruleCache.Unlock()
	assert.True(t, ruleCache.entries[key].lastAccess.After(stalePast),
		"cache hit must refresh lastAccess (kept: %v, prior: %v)",
		ruleCache.entries[key].lastAccess, stalePast)
}

// --- clusterConfigCache eviction & hit path ---

// listCountingClient wraps a real fake client and counts List calls so
// findOwnedClusterConfiguration cache-hit behavior can be asserted.
type listCountingClient struct {
	client.Client
	listCount int
}

func (c *listCountingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.listCount++
	return c.Client.List(ctx, list, opts...)
}

// TestFindOwnedClusterConfiguration_CachesResult asserts that a second
// lookup for the same (namespace, profileUID) hits the process-global
// cache rather than issuing another LIST against the regional API.
// This is the reviewer-motivated perf fix: the regional client isn't
// cache-backed, so uncached lookups amount to a real API call per
// verifier round.
func TestFindOwnedClusterConfiguration_CachesResult(t *testing.T) {
	// Snapshot + isolate the process-global CC cache.
	clusterConfigCache.Lock()
	original := maps.Clone(clusterConfigCache.entries)
	clusterConfigCache.entries = make(map[ccCacheKey]ccCacheEntry)
	clusterConfigCache.Unlock()
	t.Cleanup(func() {
		clusterConfigCache.Lock()
		clusterConfigCache.entries = original
		clusterConfigCache.Unlock()
	})

	const namespace = "cc-cache-test"
	profileUID := types.UID("profile-uid-123")

	cc := &addoncontrollerv1beta1.ClusterConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "cc-1",
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{{UID: profileUID}},
		},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, addoncontrollerv1beta1.AddToScheme(scheme))
	inner := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cc).Build()
	spy := &listCountingClient{Client: inner}

	first, err := findOwnedClusterConfiguration(context.Background(), spy, namespace, profileUID)
	require.NoError(t, err)
	require.NotNil(t, first, "first lookup must find the CC")
	require.Equal(t, 1, spy.listCount, "first lookup must LIST")

	second, err := findOwnedClusterConfiguration(context.Background(), spy, namespace, profileUID)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 1, spy.listCount, "second lookup must be served from cache (no additional LIST)")
	assert.Same(t, first, second, "cache should return the same pointer on hit")
}

// TestFindOwnedClusterConfiguration_CachesNegativeResult asserts that
// even a "not found yet" lookup is cached — this is the common case
// between Profile creation and sveltos observing the cluster, and
// re-listing the whole namespace in that window is exactly the load
// the cache is meant to shed.
func TestFindOwnedClusterConfiguration_CachesNegativeResult(t *testing.T) {
	clusterConfigCache.Lock()
	original := maps.Clone(clusterConfigCache.entries)
	clusterConfigCache.entries = make(map[ccCacheKey]ccCacheEntry)
	clusterConfigCache.Unlock()
	t.Cleanup(func() {
		clusterConfigCache.Lock()
		clusterConfigCache.entries = original
		clusterConfigCache.Unlock()
	})

	scheme := runtime.NewScheme()
	require.NoError(t, addoncontrollerv1beta1.AddToScheme(scheme))
	inner := fake.NewClientBuilder().WithScheme(scheme).Build() // no CC exists
	spy := &listCountingClient{Client: inner}

	first, err := findOwnedClusterConfiguration(context.Background(), spy, "empty-ns", types.UID("nope"))
	require.NoError(t, err)
	require.Nil(t, first)
	require.Equal(t, 1, spy.listCount)

	second, err := findOwnedClusterConfiguration(context.Background(), spy, "empty-ns", types.UID("nope"))
	require.NoError(t, err)
	assert.Nil(t, second)
	assert.Equal(t, 1, spy.listCount, "negative result must also be cached")
}

// TestSweepClusterConfigCache_EvictsStaleEntries asserts that
// sweepClusterConfigCache drops entries whose writtenAt is older than
// clusterConfigCacheTTL while preserving fresh ones. Write-based TTL —
// unlike the ruleCache, this cache does NOT refresh on read (see
// ccCacheEntry doc-comment).
func TestSweepClusterConfigCache_EvictsStaleEntries(t *testing.T) {
	clusterConfigCache.Lock()
	original := maps.Clone(clusterConfigCache.entries)
	clusterConfigCache.entries = make(map[ccCacheKey]ccCacheEntry)
	clusterConfigCache.Unlock()
	t.Cleanup(func() {
		clusterConfigCache.Lock()
		clusterConfigCache.entries = original
		clusterConfigCache.Unlock()
	})

	freshKey := ccCacheKey{namespace: "ns-a", profileUID: "fresh"}
	staleKey := ccCacheKey{namespace: "ns-b", profileUID: "stale"}
	now := time.Now()

	clusterConfigCache.Lock()
	clusterConfigCache.entries[freshKey] = ccCacheEntry{writtenAt: now.Add(-clusterConfigCacheTTL / 2)}
	clusterConfigCache.entries[staleKey] = ccCacheEntry{writtenAt: now.Add(-2 * clusterConfigCacheTTL)}
	clusterConfigCache.Unlock()

	sweepClusterConfigCache()

	clusterConfigCache.Lock()
	defer clusterConfigCache.Unlock()
	_, freshOK := clusterConfigCache.entries[freshKey]
	_, staleOK := clusterConfigCache.entries[staleKey]
	assert.True(t, freshOK, "fresh entry must survive sweep")
	assert.False(t, staleOK, "stale entry must be evicted")
}

// --- rulesFromConfigMaps ---

const systemNamespace = "kcm-system"

func makeRulesConfigMap(t *testing.T, namespace, name, target, rulesBody string) *corev1.ConfigMap {
	t.Helper()
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{healthRuleTargetLabel: target},
		},
		Data: map[string]string{healthRuleConfigMapDataKey: rulesBody},
	}
}

// testMCSOwnerName is the MultiClusterService name that every test
// ServiceSet claims as its owner. Tier-3 rule CMs point at this name to
// exercise the owner-matching path.
const testMCSOwnerName = "my-mcs"

// makeServiceSetOwnedByMCS returns a ServiceSet whose OwnerReferences
// name testMCSOwnerName — matches what the MCS reconciler stamps on
// ServiceSets it creates. Used to drive the tier-3 lookup in
// rulesFromConfigMaps tests.
func makeServiceSetOwnedByMCS(namespace, name string) *kcmv1.ServiceSet {
	return &kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: kcmv1.GroupVersion.String(),
				Kind:       kcmv1.MultiClusterServiceKind,
				Name:       testMCSOwnerName,
			}},
		},
	}
}

const podOnlyRulesYAML = `
- group: ""
  version: v1
  kind: Pod
  scope: Namespaced
  healthy: |
    has(obj.status.conditions) &&
    obj.status.conditions.exists(c, c.type == "Ready" && c.status == "True")
  message: |
    "pod not ready"
`

func TestRulesFromConfigMaps_AllThreeTiers(t *testing.T) {
	t1 := makeRulesConfigMap(t, systemNamespace, "cluster-rules", "global", podOnlyRulesYAML)
	t2 := makeRulesConfigMap(t, releaseNs, "ns-rules", "global", `
- group: apps
  version: v1
  kind: Deployment
  scope: Namespaced
  healthy: |
    has(obj.status.observedGeneration)
  message: |
    "no observedGeneration"
`)
	// Tier 3 is keyed by the ServiceSet's owner (MCS) name, not the
	// ServiceSet name itself — the ServiceSet name isn't stable enough
	// to author rules against.
	t3 := makeRulesConfigMap(t, releaseNs, "set-rules", "my-mcs", `
- group: apps
  version: v1
  kind: StatefulSet
  scope: Namespaced
  healthy: |
    has(obj.status.replicas)
  message: |
    "no replicas"
`)

	c := newFakeClient(t, t1, t2, t3)
	ss := makeServiceSetOwnedByMCS(releaseNs, "myset")
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, ss)
	require.NoError(t, err)
	assert.Empty(t, loadErrs)

	assert.Len(t, rs[schema.GroupKind{Group: "", Kind: "Pod"}], 1, "Pod from tier 1")
	assert.Len(t, rs[schema.GroupKind{Group: "apps", Kind: "Deployment"}], 1, "Deployment from tier 2")
	assert.Len(t, rs[schema.GroupKind{Group: "apps", Kind: "StatefulSet"}], 1, "StatefulSet from tier 3")
}

func TestRulesFromConfigMaps_SystemNamespaceEqualsServiceSetNamespace(t *testing.T) {
	// Self-managed scenario: SS namespace IS the system namespace. The
	// loader must not double-list the same CM.
	t1 := makeRulesConfigMap(t, systemNamespace, "cluster-rules", "global", podOnlyRulesYAML)

	c := newFakeClient(t, t1)
	ss := makeServiceSetOwnedByMCS(systemNamespace, "self-mgmt-ss")
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, ss)
	require.NoError(t, err)
	assert.Empty(t, loadErrs)
	assert.Len(t, rs[schema.GroupKind{Group: "", Kind: "Pod"}], 1, "must not duplicate")
}

func TestRulesFromConfigMaps_BadCELSurfacesAsLoadError(t *testing.T) {
	cm := makeRulesConfigMap(t, systemNamespace, "bad-rules", "global", `
- group: ""
  version: v1
  kind: Pod
  scope: Namespaced
  healthy: this is not valid CEL
  message: "bad"
`)
	c := newFakeClient(t, cm)
	ss := makeServiceSetOwnedByMCS(releaseNs, "ss")
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, ss)
	require.NoError(t, err)
	require.Len(t, loadErrs, 1)
	assert.Equal(t, "kcm-system/bad-rules#0", loadErrs[0].Source)
	// rule set has no entries because the only candidate failed to compile.
	assert.Empty(t, rs)
}

func TestRulesFromConfigMaps_NoOwnerSkipsTier3(t *testing.T) {
	// A ServiceSet without an MCS/CD owner reference should still load
	// tier 1 and tier 2 correctly; tier 3 is simply skipped.
	t1 := makeRulesConfigMap(t, systemNamespace, "cluster-rules", "global", podOnlyRulesYAML)
	tSpec := makeRulesConfigMap(t, releaseNs, "spec-rules", "some-owner", podOnlyRulesYAML)

	c := newFakeClient(t, t1, tSpec)
	ss := &kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: releaseNs},
	}
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, ss)
	require.NoError(t, err)
	assert.Empty(t, loadErrs)
	// Only tier 1's rule made it in — tier 3 was skipped because there
	// is no owner name to match, and the "spec-rules" CM's label value
	// therefore isn't queried.
	assert.Len(t, rs[schema.GroupKind{Group: "", Kind: "Pod"}], 1)
}

// --- computeServiceHash ---

func sampleHelmRelease() *addoncontrollerv1beta1.HelmChartSummary {
	return &addoncontrollerv1beta1.HelmChartSummary{
		ReleaseName:      "mcs-podinfo",
		ReleaseNamespace: "podinfo",
		ValuesHash:       []byte("values-abc"),
		PatchesHash:      []byte("patches-xyz"),
	}
}

func sampleChart() *addoncontrollerv1beta1.Chart {
	return &addoncontrollerv1beta1.Chart{
		ReleaseName:  "mcs-podinfo",
		Namespace:    "podinfo",
		ChartVersion: "6.5.0",
		AppVersion:   "6.5.0",
		RepoURL:      "GitRepository://kcm-system/podinfo-6-5-0/charts/podinfo",
	}
}

func TestComputeServiceHash_Stable(t *testing.T) {
	a := computeServiceHash(sampleHelmRelease(), sampleChart())
	b := computeServiceHash(sampleHelmRelease(), sampleChart())
	assert.NotEmpty(t, a)
	assert.Equal(t, a, b, "same input must produce same hash")
}

func TestComputeServiceHash_NilInputsReturnEmpty(t *testing.T) {
	assert.Empty(t, computeServiceHash(nil, sampleChart()))
	assert.Empty(t, computeServiceHash(sampleHelmRelease(), nil))
	assert.Empty(t, computeServiceHash(nil, nil))
}

func TestComputeServiceHash_ExcludesLastAppliedTime(t *testing.T) {
	c1 := sampleChart()
	c2 := sampleChart()
	c2.LastAppliedTime = &metav1.Time{Time: time.Now()}
	a := computeServiceHash(sampleHelmRelease(), c1)
	b := computeServiceHash(sampleHelmRelease(), c2)
	assert.Equal(t, a, b, "lastAppliedTime must NOT influence the hash")
}

func TestComputeServiceHash_DetectsEveryComponentChange(t *testing.T) {
	base := computeServiceHash(sampleHelmRelease(), sampleChart())

	cases := []struct {
		name   string
		mutate func(*addoncontrollerv1beta1.HelmChartSummary, *addoncontrollerv1beta1.Chart)
	}{
		{"chart appVersion", func(_ *addoncontrollerv1beta1.HelmChartSummary, c *addoncontrollerv1beta1.Chart) {
			c.AppVersion = "6.5.1"
		}},
		{"chart chartVersion", func(_ *addoncontrollerv1beta1.HelmChartSummary, c *addoncontrollerv1beta1.Chart) {
			c.ChartVersion = "6.6.0"
		}},
		{"chart namespace", func(_ *addoncontrollerv1beta1.HelmChartSummary, c *addoncontrollerv1beta1.Chart) {
			c.Namespace = "other-ns"
		}},
		{"chart releaseName", func(_ *addoncontrollerv1beta1.HelmChartSummary, c *addoncontrollerv1beta1.Chart) {
			c.ReleaseName = "other"
		}},
		{"chart repoURL", func(_ *addoncontrollerv1beta1.HelmChartSummary, c *addoncontrollerv1beta1.Chart) {
			c.RepoURL = "OCIRepository://elsewhere"
		}},
		{"release ValuesHash", func(r *addoncontrollerv1beta1.HelmChartSummary, _ *addoncontrollerv1beta1.Chart) {
			r.ValuesHash = []byte("values-new")
		}},
		{"release PatchesHash", func(r *addoncontrollerv1beta1.HelmChartSummary, _ *addoncontrollerv1beta1.Chart) {
			r.PatchesHash = []byte("patches-new")
		}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := sampleHelmRelease()
			c := sampleChart()
			tt.mutate(r, c)
			got := computeServiceHash(r, c)
			assert.NotEqual(t, base, got, "mutating %s must change the hash", tt.name)
		})
	}
}

// --- serviceHashesFromArtifacts ---

func TestServiceHashesFromArtifacts_NilInputs(t *testing.T) {
	assert.Empty(t, serviceHashesFromArtifacts(nil, nil, addoncontrollerv1beta1.ClusterProfileKind, "p"))
	assert.Empty(t, serviceHashesFromArtifacts(nil, &addoncontrollerv1beta1.ClusterConfiguration{}, addoncontrollerv1beta1.ClusterProfileKind, "p"))
	assert.Empty(t, serviceHashesFromArtifacts(&addoncontrollerv1beta1.ClusterSummary{}, nil, addoncontrollerv1beta1.ClusterProfileKind, "p"))
}

func TestServiceHashesFromArtifacts_BuildsMapForClusterProfile(t *testing.T) {
	summary := &addoncontrollerv1beta1.ClusterSummary{
		Status: addoncontrollerv1beta1.ClusterSummaryStatus{
			HelmReleaseSummaries: []addoncontrollerv1beta1.HelmChartSummary{
				{ReleaseName: "mcs-podinfo", ReleaseNamespace: "podinfo", ValuesHash: []byte("v1"), PatchesHash: []byte("p1")},
				{ReleaseName: "mcs-prometheus", ReleaseNamespace: "monitoring", ValuesHash: []byte("v2"), PatchesHash: []byte("p2")},
			},
		},
	}
	cc := &addoncontrollerv1beta1.ClusterConfiguration{
		Status: addoncontrollerv1beta1.ClusterConfigurationStatus{
			ClusterProfileResources: []addoncontrollerv1beta1.ClusterProfileResource{
				{
					ClusterProfileName: "myset",
					Features: []addoncontrollerv1beta1.Feature{
						{
							FeatureID: libsveltosv1beta1.FeatureHelm,
							Charts: []addoncontrollerv1beta1.Chart{
								{ReleaseName: "mcs-podinfo", Namespace: "podinfo", ChartVersion: "6.5.0", AppVersion: "6.5.0", RepoURL: "git://podinfo"},
								{ReleaseName: "mcs-prometheus", Namespace: "monitoring", ChartVersion: "27.1.0", AppVersion: "27.1.0", RepoURL: "git://prom"},
							},
						},
					},
				},
			},
		},
	}

	hashes := serviceHashesFromArtifacts(summary, cc, addoncontrollerv1beta1.ClusterProfileKind, "myset")
	require.Len(t, hashes, 2)
	assert.NotEmpty(t, hashes[client.ObjectKey{Namespace: "podinfo", Name: "mcs-podinfo"}])
	assert.NotEmpty(t, hashes[client.ObjectKey{Namespace: "monitoring", Name: "mcs-prometheus"}])
	assert.NotEqual(t,
		hashes[client.ObjectKey{Namespace: "podinfo", Name: "mcs-podinfo"}],
		hashes[client.ObjectKey{Namespace: "monitoring", Name: "mcs-prometheus"}],
		"distinct services must hash distinctly")
}

func TestServiceHashesFromArtifacts_SkipsReleaseWithoutChartEntry(t *testing.T) {
	// HelmReleaseSummaries lists a release that ClusterConfiguration has
	// no Charts[] entry for. The release is skipped.
	summary := &addoncontrollerv1beta1.ClusterSummary{
		Status: addoncontrollerv1beta1.ClusterSummaryStatus{
			HelmReleaseSummaries: []addoncontrollerv1beta1.HelmChartSummary{
				{ReleaseName: "no-chart", ReleaseNamespace: "ns", ValuesHash: []byte("x")},
			},
		},
	}
	cc := &addoncontrollerv1beta1.ClusterConfiguration{
		Status: addoncontrollerv1beta1.ClusterConfigurationStatus{
			ClusterProfileResources: []addoncontrollerv1beta1.ClusterProfileResource{{
				ClusterProfileName: "myset",
				Features:           []addoncontrollerv1beta1.Feature{{FeatureID: libsveltosv1beta1.FeatureHelm}},
			}},
		},
	}
	hashes := serviceHashesFromArtifacts(summary, cc, addoncontrollerv1beta1.ClusterProfileKind, "myset")
	assert.Empty(t, hashes)
}

func TestServiceHashesFromArtifacts_WrongProfileSectionReturnsEmpty(t *testing.T) {
	cc := &addoncontrollerv1beta1.ClusterConfiguration{
		Status: addoncontrollerv1beta1.ClusterConfigurationStatus{
			ClusterProfileResources: []addoncontrollerv1beta1.ClusterProfileResource{{
				ClusterProfileName: "different-set",
				Features:           []addoncontrollerv1beta1.Feature{{FeatureID: libsveltosv1beta1.FeatureHelm}},
			}},
		},
	}
	summary := &addoncontrollerv1beta1.ClusterSummary{}
	// Asking for "myset" but CC only has "different-set" → no section, no hashes.
	hashes := serviceHashesFromArtifacts(summary, cc, addoncontrollerv1beta1.ClusterProfileKind, "myset")
	assert.Empty(t, hashes)
}

func TestRulesFromConfigMaps_TargetLabelSelectivity(t *testing.T) {
	matching := makeRulesConfigMap(t, releaseNs, "for-us", "my-mcs", podOnlyRulesYAML)
	otherOwner := makeRulesConfigMap(t, releaseNs, "for-other", "different-mcs", podOnlyRulesYAML)
	unlabeled := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "no-label", Namespace: releaseNs},
		Data:       map[string]string{healthRuleConfigMapDataKey: podOnlyRulesYAML},
	}

	c := newFakeClient(t, matching, otherOwner, unlabeled)
	ss := makeServiceSetOwnedByMCS(releaseNs, "myset")
	rs, _, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, ss)
	require.NoError(t, err)
	require.Len(t, rs[schema.GroupKind{Group: "", Kind: "Pod"}], 1,
		"only the CM whose label value matches the ServiceSet's owner name should contribute")
}

// --- version-resolution helpers ---

// stubRESTMapper is a minimal meta.RESTMapper for resolveServedVersion
// tests. Only RESTMappings is meaningfully implemented — the other
// methods return zero values and are never called by the code under test.
type stubRESTMapper struct {
	// perGK maps a GroupKind to the versions the "cluster" serves.
	perGK map[schema.GroupKind][]string
	// perGKErr maps a GroupKind to an error the mapper should return
	// (used for NoMatch and permission-denied cases).
	perGKErr map[schema.GroupKind]error
	// perGKScope overrides the scope RESTMappings emits for a GK.
	// Default is Namespaced when not set.
	perGKScope map[schema.GroupKind]meta.RESTScope
}

func (*stubRESTMapper) KindFor(schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (*stubRESTMapper) KindsFor(schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, nil
}

func (*stubRESTMapper) ResourceFor(schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, nil
}

func (*stubRESTMapper) ResourcesFor(schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, nil
}

func (*stubRESTMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, nil //nolint:nilnil // unused stub method
}

func (m *stubRESTMapper) RESTMappings(gk schema.GroupKind, _ ...string) ([]*meta.RESTMapping, error) {
	if err, ok := m.perGKErr[gk]; ok {
		return nil, err
	}
	versions := m.perGK[gk]
	if len(versions) == 0 {
		return nil, &meta.NoKindMatchError{GroupKind: gk}
	}
	scope := meta.RESTScope(meta.RESTScopeNamespace)
	if s, ok := m.perGKScope[gk]; ok {
		scope = s
	}
	out := make([]*meta.RESTMapping, 0, len(versions))
	for _, v := range versions {
		out = append(out, &meta.RESTMapping{
			GroupVersionKind: schema.GroupVersionKind{Group: gk.Group, Version: v, Kind: gk.Kind},
			Scope:            scope,
		})
	}
	return out, nil
}

func (*stubRESTMapper) ResourceSingularizer(string) (string, error) {
	return "", nil
}

func TestDistinctVersions_PreservesFirstSeenOrderAndDedups(t *testing.T) {
	rules := []resourceRule{
		{GVK: schema.GroupVersionKind{Version: "v1beta1"}},
		{GVK: schema.GroupVersionKind{Version: "v1"}},
		{GVK: schema.GroupVersionKind{Version: "v1beta1"}}, // duplicate of the first
		{GVK: schema.GroupVersionKind{Version: "v2"}},
	}
	got := distinctVersions(rules)
	assert.Equal(t, []string{"v1beta1", "v1", "v2"}, got,
		"first-seen order preserved, duplicates removed")
}

func TestResolveServedVersion_AuthoredAndServedIntersect(t *testing.T) {
	gk := schema.GroupKind{Group: "apps", Kind: "Deployment"}
	mapper := &stubRESTMapper{perGK: map[schema.GroupKind][]string{gk: {"v1"}}}

	got, scope, err := resolveServedVersion(mapper, gk, []string{"v1"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "v1", got.Version)
	assert.Equal(t, "DeploymentList", got.Kind, "listing GVK must carry the 'List' suffix")
	assert.Equal(t, scopeNamespaced, scope, "mapper default is Namespaced")
}

// TestResolveServedVersion_ReturnsClusterScope asserts that a RESTMapper
// reporting Root scope surfaces as scopeCluster. This is the load-bearing
// return value for the scope-mismatch rejection in collectVerdicts:
// mis-declared rules can't force a cross-namespace List.
func TestResolveServedVersion_ReturnsClusterScope(t *testing.T) {
	gk := schema.GroupKind{Kind: "PersistentVolume"}
	mapper := &stubRESTMapper{
		perGK:      map[schema.GroupKind][]string{gk: {"v1"}},
		perGKScope: map[schema.GroupKind]meta.RESTScope{gk: meta.RESTScopeRoot},
	}

	got, scope, err := resolveServedVersion(mapper, gk, []string{"v1"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, scopeCluster, scope)
}

func TestResolveServedVersion_AuthoredOrderPreservedOnServedIntersection(t *testing.T) {
	gk := schema.GroupKind{Group: "apps", Kind: "Deployment"}
	// Both v1beta1 and v1 are served; author wrote v1beta1 first, so we
	// should pick v1beta1 regardless of how the mapper orders its output.
	mapper := &stubRESTMapper{perGK: map[schema.GroupKind][]string{gk: {"v1", "v1beta1"}}}

	got, _, err := resolveServedVersion(mapper, gk, []string{"v1beta1", "v1"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "v1beta1", got.Version, "authored first-seen order beats mapper order")
}

func TestResolveServedVersion_NoIntersectionReturnsNilNil(t *testing.T) {
	gk := schema.GroupKind{Group: "apps", Kind: "Deployment"}
	// Cluster serves v1 only; author wrote v1beta1 only.
	mapper := &stubRESTMapper{perGK: map[schema.GroupKind][]string{gk: {"v1"}}}

	got, scope, err := resolveServedVersion(mapper, gk, []string{"v1beta1"})
	require.NoError(t, err, "version mismatch is a silent-skip, not an error")
	assert.Nil(t, got, "no served version matches → nil listing GVK")
	assert.Empty(t, scope, "no served version matches → empty scope")
}

func TestResolveServedVersion_KindNotServedReturnsNilNil(t *testing.T) {
	gk := schema.GroupKind{Group: "widgets.example.com", Kind: "Widget"}
	// No entry in perGK → stub returns NoKindMatchError.
	mapper := &stubRESTMapper{}

	got, scope, err := resolveServedVersion(mapper, gk, []string{"v1"})
	require.NoError(t, err, "NoKindMatch is a silent-skip, not an error")
	assert.Nil(t, got)
	assert.Empty(t, scope)
}

func TestResolveServedVersion_OtherMapperErrorSurfaces(t *testing.T) {
	gk := schema.GroupKind{Group: "apps", Kind: "Deployment"}
	mapper := &stubRESTMapper{
		perGKErr: map[schema.GroupKind]error{gk: errors.New("permission denied")},
	}

	got, _, err := resolveServedVersion(mapper, gk, []string{"v1"})
	require.Error(t, err, "non-NoMatch mapper errors must surface to the caller")
	require.ErrorContains(t, err, "permission denied")
	assert.Nil(t, got)
}

func TestFormatGroupVersionKind(t *testing.T) {
	cases := []struct {
		name    string
		gk      schema.GroupKind
		version string
		want    string
	}{
		{
			name:    "non-empty group",
			gk:      schema.GroupKind{Group: "apps", Kind: "Deployment"},
			version: "v1",
			want:    "apps/v1/Deployment",
		},
		{
			name:    "empty group renders as core",
			gk:      schema.GroupKind{Group: "", Kind: "Pod"},
			version: "v1",
			want:    "core/v1/Pod",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatGroupVersionKind(tc.gk, tc.version))
		})
	}
}

// TestTruncateReason_TrimsAndCaps asserts the two bounded behaviors:
// leading/trailing whitespace is stripped (multi-line CEL messages
// render single-line), and long reasons are capped at
// maxVerdictReasonBytes with an ellipsis suffix. Guards against
// a runaway user CEL message pushing a ServiceHealth<Kind> condition
// past the CRD's 32 KiB per-message limit.
func TestTruncateReason_TrimsAndCaps(t *testing.T) {
	t.Run("trims whitespace", func(t *testing.T) {
		assert.Equal(t, "pod not ready", truncateReason("  pod not ready\n"))
	})
	t.Run("short reason passes through", func(t *testing.T) {
		assert.Equal(t, "phase=Pending", truncateReason("phase=Pending"))
	})
	t.Run("long reason is capped with ellipsis", func(t *testing.T) {
		long := strings.Repeat("x", maxVerdictReasonBytes+100)
		got := truncateReason(long)
		assert.Len(t, got, maxVerdictReasonBytes+len("…"))
		assert.True(t, strings.HasSuffix(got, "…"), "must end with ellipsis")
		assert.Equal(t, strings.Repeat("x", maxVerdictReasonBytes), got[:maxVerdictReasonBytes],
			"leading maxVerdictReasonBytes must survive unchanged")
	})
}

// TestApplyRuleLoadErrorCondition_BoundsMessage asserts the two caps on
// the ServiceSetHealthRules condition message: max
// maxRuleLoadErrorsInCondition individual errors enumerated (rest
// summarised with "…and N more"), and the total message truncated to
// maxRuleLoadErrorConditionMessageBytes. Both mechanisms exist so a
// misconfigured user rule set can't produce a condition message that
// exceeds the CRD's 32 KiB limit and blocks every status write.
func TestApplyRuleLoadErrorCondition_BoundsMessage(t *testing.T) {
	t.Run("count cap enumerates first N and summarises rest", func(t *testing.T) {
		errs := make([]ruleLoadError, 0, maxRuleLoadErrorsInCondition+5)
		for i := range maxRuleLoadErrorsInCondition + 5 {
			errs = append(errs, ruleLoadError{
				Source: fmt.Sprintf("ns/cm#%d", i),
				Err:    errors.New("boom"),
			})
		}
		ss := &kcmv1.ServiceSet{}
		applyRuleLoadErrorCondition(ss, errs)
		require.Len(t, ss.Status.Conditions, 1)
		msg := ss.Status.Conditions[0].Message
		assert.Contains(t, msg, "ns/cm#0")
		assert.Contains(t, msg, fmt.Sprintf("ns/cm#%d", maxRuleLoadErrorsInCondition-1),
			"last enumerated entry must be the Nth")
		assert.NotContains(t, msg, fmt.Sprintf("ns/cm#%d", maxRuleLoadErrorsInCondition),
			"beyond the cap, entries are summarised, not enumerated")
		assert.Contains(t, msg, "…and 5 more")
	})

	t.Run("byte cap truncates when a single error is huge", func(t *testing.T) {
		// One error whose stringified form alone exceeds the byte cap.
		huge := strings.Repeat("x", maxRuleLoadErrorConditionMessageBytes+1024)
		ss := &kcmv1.ServiceSet{}
		applyRuleLoadErrorCondition(ss, []ruleLoadError{{
			Source: "ns/cm#huge",
			Err:    errors.New(huge),
		}})
		require.Len(t, ss.Status.Conditions, 1)
		msg := ss.Status.Conditions[0].Message
		// Cap+1 because the truncator appends "…" past the cap.
		assert.LessOrEqual(t, len(msg), maxRuleLoadErrorConditionMessageBytes+len("…"),
			"message must not exceed byte cap + ellipsis")
		assert.True(t, strings.HasSuffix(msg, "…"), "truncated message must end with ellipsis")
	})

	t.Run("no errors yields success condition", func(t *testing.T) {
		ss := &kcmv1.ServiceSet{}
		applyRuleLoadErrorCondition(ss, nil)
		require.Len(t, ss.Status.Conditions, 1)
		assert.Equal(t, metav1.ConditionTrue, ss.Status.Conditions[0].Status)
		assert.Equal(t, "AllRulesValid", ss.Status.Conditions[0].Reason)
	})
}
