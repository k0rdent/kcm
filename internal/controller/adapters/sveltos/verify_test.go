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
	"fmt"
	"testing"
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
        string(int(has(obj.spec.replicas) ? obj.spec.replicas : 1)) + " replicas ready"
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
    "statefulset not ready"
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
    "daemonset not ready"
- group: ""
  version: v1
  kind: Pod
  scope: Namespaced
  healthy: |
    has(obj.status.conditions) &&
    obj.status.conditions.exists(c, c.type == "Ready" && c.status == "True")
  message: |
    "pod not ready"
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
	rs, errs := rulesFromDocs(docs)
	if len(errs) > 0 {
		panic(fmt.Errorf("test rules failed to compile: %v", errs))
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
	healthy, reason, err := rule.evaluate(u)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthy, _ := evalRule(t, "", "Pod", tt.payload)
			assert.Equal(t, tt.wantHealthy, healthy)
		})
	}
}

func TestRule_Pod_TerminatingIgnored(t *testing.T) {
	rules := testRules[schema.GroupKind{Group: "", Kind: "Pod"}]
	require.Len(t, rules, 1)
	rule := rules[0]
	u := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "False"},
			},
		},
	}}
	u.SetGroupVersionKind(rule.GVK)
	u.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})

	healthy, _, err := rule.evaluate(u)
	require.NoError(t, err)
	assert.True(t, healthy, "terminating pod must be treated as healthy")
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
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
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

func TestVerifyHelmServiceOnCluster_NoResources(t *testing.T) {
	c := newFakeClient(t)
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateFailed, state)
	require.Len(t, conds, 1)
	assert.Equal(t, "NoResourcesOnCluster", conds[0].Reason)
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

func TestVerifyHelmServiceOnCluster_OtherReleaseIgnored(t *testing.T) {
	other := makeHealthyDeployment("other")
	other.Labels["release"] = "different"

	c := newFakeClient(t, other)
	state, _, err := verifyHelmServiceOnCluster(context.Background(), c, testRules, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateFailed, state)
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
	rs, errs := rulesFromDocs(docs)
	require.Empty(t, errs)
	require.Len(t, rs[schema.GroupKind{Group: "apps", Kind: "Deployment"}], 2)

	c := newFakeClient(t, makeHealthyDeployment("web"))
	state, conds, err := verifyHelmServiceOnCluster(context.Background(), c, rs, releaseName, releaseNs)
	require.NoError(t, err)
	assert.Equal(t, kcmv1.ServiceStateProvisioning, state, "any unhealthy rule must downgrade")
	require.Len(t, conds, 1)
	assert.Contains(t, conds[0].Message, "always unhealthy")
	assert.Contains(t, conds[0].Message, "test-extra#0", "failing rule's source must appear in the condition")
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
	types := []string{conds[0].Type, conds[1].Type}
	assert.Contains(t, types, "ServiceHealthDeployment")
	assert.Contains(t, types, "ServiceHealthPod")
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
