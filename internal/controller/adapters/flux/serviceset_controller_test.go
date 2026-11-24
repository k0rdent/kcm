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

package flux

import (
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func helmTemplate() *kcmv1.ServiceTemplate {
	return &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress-nginx", Namespace: "kcm-system"},
		Spec: kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "ingress-nginx", Version: "4.11.0"}},
		},
		Status: kcmv1.ServiceTemplateStatus{
			TemplateStatusCommon: kcmv1.TemplateStatusCommon{
				ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
					Kind:      "HelmChart",
					Name:      "kcm-system-ingress-nginx",
					Namespace: "kcm-system",
				},
			},
		},
	}
}

func TestBuildHelmRelease(t *testing.T) {
	serviceSet := &kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-mcs", Namespace: "kcm-system"},
	}
	svc := kcmv1.ServiceWithValues{
		Name:      "ingress-nginx",
		Namespace: "ingress",
		Template:  "ingress-nginx",
		Values:    "controller:\n  replicaCount: 2\n",
		ValuesFrom: []kcmv1.ValuesFrom{
			{Kind: "ConfigMap", Name: "extra-values"},
		},
	}

	hr, err := buildHelmRelease(serviceSet, svc, helmTemplate(), providerConfig{}, nil, helmReleaseName(serviceSet, svc))
	require.NoError(t, err)

	require.Equal(t, "cluster-mcs-ingress-ingress-nginx", hr.Name)
	require.Equal(t, "kcm-system", hr.Namespace)
	require.Equal(t, kcmv1.KCMManagedLabelValue, hr.Labels[kcmv1.KCMManagedLabelKey])
	require.Equal(t, "ingress-nginx", hr.Labels[serviceNameLabel])
	require.Equal(t, "ingress", hr.Labels[serviceNamespaceLabel])

	require.Equal(t, "ingress-nginx", hr.Spec.ReleaseName)
	require.Equal(t, "ingress", hr.Spec.TargetNamespace)
	require.Equal(t, "ingress", hr.Spec.StorageNamespace)
	require.Equal(t, metav1.Duration{Duration: defaultInterval}, hr.Spec.Interval)
	require.Nil(t, hr.Spec.KubeConfig)
	require.True(t, hr.Spec.Install.CreateNamespace)

	require.NotNil(t, hr.Spec.ChartRef)
	require.Equal(t, "HelmChart", hr.Spec.ChartRef.Kind)
	require.Equal(t, "kcm-system-ingress-nginx", hr.Spec.ChartRef.Name)

	require.JSONEq(t, `{"controller":{"replicaCount":2}}`, string(hr.Spec.Values.Raw))
	require.Equal(t, []helmcontrollerv2.ValuesReference{{Kind: "ConfigMap", Name: "extra-values"}}, hr.Spec.ValuesFrom)
}

// A paused ServiceSet must stop helm-controller, not just KCM: suspend is the
// only thing that keeps a release from being reconciled behind our back.
func TestBuildHelmReleaseSuspendsOnPause(t *testing.T) {
	serviceSet := &kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster-mcs",
			Namespace:   "kcm-system",
			Annotations: map[string]string{kcmv1.ServiceSetPausedAnnotation: "true"},
		},
	}
	svc := kcmv1.ServiceWithValues{Name: "svc", Namespace: "ns", Template: "ingress-nginx"}

	hr, err := buildHelmRelease(serviceSet, svc, helmTemplate(), providerConfig{}, nil, "hr")
	require.NoError(t, err)
	require.True(t, hr.Spec.Suspend)
}

func TestBuildHelmReleaseProviderConfig(t *testing.T) {
	serviceSet := &kcmv1.ServiceSet{ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "kcm-system"}}
	svc := kcmv1.ServiceWithValues{Name: "svc", Namespace: "ns", Template: "ingress-nginx"}

	cfg, err := providerConfigFromJSON(&apiextv1.JSON{
		Raw: []byte(`{"interval":"2m","serviceAccountName":"deployer","driftDetectionMode":"warn","syncMode":"Continuous"}`),
	})
	require.NoError(t, err)

	hr, err := buildHelmRelease(serviceSet, svc, helmTemplate(), cfg, nil, "hr")
	require.NoError(t, err)
	require.Equal(t, metav1.Duration{Duration: 2 * time.Minute}, hr.Spec.Interval)
	require.Equal(t, "deployer", hr.Spec.ServiceAccountName)
	require.Equal(t, helmcontrollerv2.DriftDetectionWarn, hr.Spec.DriftDetection.Mode)
}

func TestServiceStateFromHelmRelease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		conditions []metav1.Condition
		wantState  string
		wantMsg    string
	}{
		{
			name:      "no conditions yet",
			wantState: kcmv1.ServiceStateProvisioning,
		},
		{
			name:       "ready",
			conditions: []metav1.Condition{{Type: fluxmeta.ReadyCondition, Status: metav1.ConditionTrue, Reason: helmcontrollerv2.InstallSucceededReason}},
			wantState:  kcmv1.ServiceStateDeployed,
		},
		{
			name: "install failed",
			conditions: []metav1.Condition{{
				Type: fluxmeta.ReadyCondition, Status: metav1.ConditionFalse,
				Reason: helmcontrollerv2.InstallFailedReason, Message: "no CRDs",
			}},
			wantState: kcmv1.ServiceStateFailed,
			wantMsg:   "no CRDs",
		},
		{
			// Reconciliation in flight is not a failure; reporting it as one
			// would make every install flap through Failed on its way up.
			name: "still reconciling",
			conditions: []metav1.Condition{{
				Type: fluxmeta.ReadyCondition, Status: metav1.ConditionFalse,
				Reason: "Progressing", Message: "running install",
			}},
			wantState: kcmv1.ServiceStateProvisioning,
		},
		{
			name: "stalled wins over ready",
			conditions: []metav1.Condition{
				{Type: fluxmeta.ReadyCondition, Status: metav1.ConditionTrue},
				{Type: fluxmeta.StalledCondition, Status: metav1.ConditionTrue, Message: "retries exhausted"},
			},
			wantState: kcmv1.ServiceStateFailed,
			wantMsg:   "retries exhausted",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hr := &helmcontrollerv2.HelmRelease{Status: helmcontrollerv2.HelmReleaseStatus{Conditions: tc.conditions}}
			state, msg := serviceStateFromHelmRelease(hr)
			require.Equal(t, tc.wantState, state)
			require.Equal(t, tc.wantMsg, msg)
		})
	}
}

func TestHelmReleaseNameIsBounded(t *testing.T) {
	serviceSet := &kcmv1.ServiceSet{ObjectMeta: metav1.ObjectMeta{Name: longString(200)}}
	svc := kcmv1.ServiceWithValues{Name: longString(200), Namespace: longString(200)}
	name := helmReleaseName(serviceSet, svc)
	require.LessOrEqual(t, len(name), 253)
	require.NotEqual(t, name, helmReleaseName(serviceSet, kcmv1.ServiceWithValues{
		Name: longString(200), Namespace: longString(199) + "x",
	}))
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
