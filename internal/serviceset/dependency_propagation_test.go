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

package serviceset

import (
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// The KSM-207 shape: a three-deep dependsOn chain inside one MultiClusterService,
// where two of the entries share a template family.
const (
	depChainSystemNamespace = "kcm-system"
	depChainServiceNS       = "k0rdent-apis"

	catalogOld = "kacs-k0r-ai-catalog-1.0.0"
	catalogNew = "kacs-k0r-ai-catalog-1.1.0-rc.1"
	authAPIOld = "kacs-auth-api-1.0.1"
	authAPINew = "kacs-auth-api-1.1.0-rc.1"

	svcA = "kacs-auth-api"                   // no dependsOn
	svcB = "kacs-mothership-auth-api"        // dependsOn A
	svcC = "kacs-mothership-pc-cfg-auth-api" // dependsOn B
)

func depChainTemplate(name, version string) *kcmv1.ServiceTemplate {
	return &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: depChainSystemNamespace},
		Spec: kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "chart", Version: version}},
		},
		Status: kcmv1.ServiceTemplateStatus{
			TemplateStatusCommon: kcmv1.TemplateStatusCommon{
				TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true},
			},
		},
	}
}

func depChainMCS(catalogTemplate, authAPITemplate string) *kcmv1.MultiClusterService {
	return &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Name: "kacs-mothership-auth-api"},
		Spec: kcmv1.MultiClusterServiceSpec{
			ServiceSpec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:           "ksm-projectsveltos",
					SelfManagement: true,
				},
				Services: []kcmv1.Service{
					{Name: svcA, Namespace: depChainServiceNS, Template: catalogTemplate},
					{
						Name: svcB, Namespace: depChainServiceNS, Template: authAPITemplate,
						DependsOn: []kcmv1.ServiceDependsOn{{Name: svcA, Namespace: depChainServiceNS}},
					},
					{
						Name: svcC, Namespace: depChainServiceNS, Template: catalogTemplate,
						DependsOn: []kcmv1.ServiceDependsOn{{Name: svcB, Namespace: depChainServiceNS}},
					},
				},
			},
		},
	}
}

type depChainHarness struct {
	t   *testing.T
	cl  client.Client
	key client.ObjectKey
	// pinnedVersions holds services whose reported Status.Version is frozen at
	// the given value, simulating an adapter that stopped advancing the field.
	pinnedVersions map[string]string
}

func newDepChainHarness(t *testing.T) *depChainHarness {
	t.Helper()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	mcs := depChainMCS(catalogOld, authAPIOld)
	provider := &kcmv1.StateManagementProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "ksm-projectsveltos"},
		Spec: kcmv1.StateManagementProviderSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"adapter": "kcm"}},
			Adapter: kcmv1.ResourceReference{
				APIVersion: "v1", Kind: "Deployment", Name: "adapter", Namespace: "adapter-ns",
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			depChainTemplate(catalogOld, "1.0.0"),
			depChainTemplate(catalogNew, "1.1.0-rc.1"),
			depChainTemplate(authAPIOld, "1.0.1"),
			depChainTemplate(authAPINew, "1.1.0-rc.1"),
			provider, mcs,
		).
		WithStatusSubresource(&kcmv1.ServiceSet{}).
		WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
		WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
		Build()

	return &depChainHarness{
		t:              t,
		cl:             cl,
		key:            ObjectKey(depChainSystemNamespace, nil, mcs),
		pinnedVersions: map[string]string{},
	}
}

// reconcile runs one MultiClusterService reconcile of the ServiceSet, then one
// adapter status collection. The collector models the adapter contract stated in
// FilterServiceDependencies' CONDITIONS (5): status converges to spec.
func (h *depChainHarness) reconcile(mcs *kcmv1.MultiClusterService) {
	h.t.Helper()

	serviceSet, op, err := GetServiceSetWithOperation(h.t.Context(), h.cl, OperationRequisites{
		ObjectKey:       h.key,
		MCS:             mcs,
		SystemNamespace: depChainSystemNamespace,
	})
	require.NoError(h.t, err)
	require.NoError(h.t, NewProcessor(h.cl).CreateOrUpdateServiceSet(h.t.Context(), op, serviceSet))

	stored := new(kcmv1.ServiceSet)
	require.NoError(h.t, h.cl.Get(h.t.Context(), h.key, stored))

	states := make([]kcmv1.ServiceState, 0, len(stored.Spec.Services))
	for _, svc := range stored.Spec.Services {
		version := svc.Version
		if pinned, ok := h.pinnedVersions[svc.Name]; ok {
			version = new(pinned)
		}
		states = append(states, kcmv1.ServiceState{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Template:  svc.Template,
			Version:   version,
			Type:      kcmv1.ServiceTypeHelm,
			State:     kcmv1.ServiceStateDeployed,
		})
	}
	stored.Status.Services = states
	stored.Status.Deployed = true
	require.NoError(h.t, h.cl.Status().Update(h.t.Context(), stored))
}

// templates returns the template each service is pinned to in the ServiceSet spec.
func (h *depChainHarness) templates() map[string]string {
	h.t.Helper()

	stored := new(kcmv1.ServiceSet)
	require.NoError(h.t, h.cl.Get(h.t.Context(), h.key, stored))

	got := make(map[string]string, len(stored.Spec.Services))
	for _, svc := range stored.Spec.Services {
		got[svc.Name] = svc.Template
	}
	return got
}

func (h *depChainHarness) converge(mcs *kcmv1.MultiClusterService, rounds int) {
	h.t.Helper()

	for range rounds {
		h.reconcile(mcs)
	}
}

// KSM-207: every service carrying a dependsOn stayed pinned to its old template
// forever while the one without a dependsOn updated normally, with no error and
// no condition anywhere to show for it.
func Test_ServiceDependencyChain_PropagatesTemplateBump(t *testing.T) {
	t.Parallel()

	h := newDepChainHarness(t)
	h.converge(depChainMCS(catalogOld, authAPIOld), 4)

	require.Equal(t, map[string]string{svcA: catalogOld, svcB: authAPIOld, svcC: catalogOld}, h.templates(),
		"precondition: the chain is fully deployed at the old templates")

	bumped := depChainMCS(catalogNew, authAPINew)

	// The gate is deliberately one-hop-per-reconcile: a dependency must be
	// observed Deployed at its new version before its dependents may advance.
	h.reconcile(bumped)
	assert.Equal(t, map[string]string{svcA: catalogNew, svcB: authAPIOld, svcC: catalogOld}, h.templates(),
		"round 1: only the dependency-free service may advance")

	h.reconcile(bumped)
	assert.Equal(t, map[string]string{svcA: catalogNew, svcB: authAPINew, svcC: catalogOld}, h.templates(),
		"round 2: B advances once A is deployed at its new version")

	h.reconcile(bumped)
	assert.Equal(t, map[string]string{svcA: catalogNew, svcB: authAPINew, svcC: catalogNew}, h.templates(),
		"round 3: C advances once B is deployed at its new version")

	// And it stays there — no oscillation once converged.
	h.converge(bumped, 3)
	assert.Equal(t, map[string]string{svcA: catalogNew, svcB: authAPINew, svcC: catalogNew}, h.templates())
}

// The gate reads Status.Version as liveness, so an adapter that stops advancing
// the field holds the whole chain back indefinitely. That is the failure mode
// behind KSM-207 and the reason for CONDITIONS (5) on FilterServiceDependencies:
// it is the adapter's job to keep Status.Version converging to Spec.Version.
// See Test_verifyServiceStates_NoRules_KeepsVersionLive for the adapter side.
func Test_ServiceDependencyChain_BlockedByAdapterPinningStatusVersion(t *testing.T) {
	t.Parallel()

	h := newDepChainHarness(t)
	h.converge(depChainMCS(catalogOld, authAPIOld), 4)

	// The adapter stops advancing Status.Version for the head of the chain.
	h.pinnedVersions[svcA] = "1.0.0"

	bumped := depChainMCS(catalogNew, authAPINew)
	h.converge(bumped, 8)

	assert.Equal(t, map[string]string{svcA: catalogNew, svcB: authAPIOld, svcC: catalogOld}, h.templates(),
		"a pinned Status.Version on the dependency freezes every dependent, silently and indefinitely")
}
