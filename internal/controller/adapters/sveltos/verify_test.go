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
	assert.Equal(t, len(want), len(testRules), "extra rules present")
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

func makeUnhealthyDeployment(name string) *appsv1.Deployment {
	d := makeHealthyDeployment(name)
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
	c := newFakeClient(t, makeUnhealthyDeployment("web"))
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
	d := makeUnhealthyDeployment("web")
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
	d := makeUnhealthyDeployment("web")
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
	// ServiceSet-targeted: name "myset" in releaseNs.
	t3 := makeRulesConfigMap(t, releaseNs, "set-rules", "myset", `
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
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, releaseNs, "myset")
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
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, systemNamespace, "self-mgmt-ss")
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
	rs, loadErrs, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, releaseNs, "ss")
	require.NoError(t, err)
	require.Len(t, loadErrs, 1)
	assert.Equal(t, "kcm-system/bad-rules#0", loadErrs[0].Source)
	// rule set has no entries because the only candidate failed to compile.
	assert.Empty(t, rs)
}

func TestRulesFromConfigMaps_TargetLabelSelectivity(t *testing.T) {
	matching := makeRulesConfigMap(t, releaseNs, "for-us", "myset", podOnlyRulesYAML)
	otherSs := makeRulesConfigMap(t, releaseNs, "for-other", "different-ss", podOnlyRulesYAML)
	unlabeled := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "no-label", Namespace: releaseNs},
		Data:       map[string]string{healthRuleConfigMapDataKey: podOnlyRulesYAML},
	}

	c := newFakeClient(t, matching, otherSs, unlabeled)
	rs, _, err := rulesFromConfigMaps(context.Background(), c, systemNamespace, releaseNs, "myset")
	require.NoError(t, err)
	require.Len(t, rs[schema.GroupKind{Group: "", Kind: "Pod"}], 1,
		"only the CM whose label value matches the ServiceSet name should contribute")
}
