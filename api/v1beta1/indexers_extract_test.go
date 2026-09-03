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

package v1beta1

import (
	"reflect"
	"testing"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtractTemplateNameFromClusterDeployment(t *testing.T) {
	if got := ExtractTemplateNameFromClusterDeployment(&ClusterDeployment{Spec: ClusterDeploymentSpec{Template: "tpl1"}}); !reflect.DeepEqual(got, []string{"tpl1"}) {
		t.Errorf("got %v, want [tpl1]", got)
	}
	if got := ExtractTemplateNameFromClusterDeployment(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterDeployment object", got)
	}
}

func TestExtractServiceTemplateNamesFromClusterDeployment(t *testing.T) {
	cd := &ClusterDeployment{Spec: ClusterDeploymentSpec{ServiceSpec: ServiceSpec{
		Services: []Service{{Template: "svc1"}, {Template: "svc2"}},
	}}}
	if got := ExtractServiceTemplateNamesFromClusterDeployment(cd); !reflect.DeepEqual(got, []string{"svc1", "svc2"}) {
		t.Errorf("got %v, want [svc1 svc2]", got)
	}
	if got := ExtractServiceTemplateNamesFromClusterDeployment(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterDeployment object", got)
	}
}

func TestExtractServiceTemplateChainNameFromClusterDeployment(t *testing.T) {
	cd := &ClusterDeployment{Spec: ClusterDeploymentSpec{ServiceSpec: ServiceSpec{
		Services: []Service{{Template: "svc1", TemplateChain: "chain1"}, {Template: "svc2"}},
	}}}
	if got := ExtractServiceTemplateChainNameFromClusterDeployment(cd); !reflect.DeepEqual(got, []string{"chain1"}) {
		t.Errorf("got %v, want [chain1] (svc2 has no chain)", got)
	}
	if got := ExtractServiceTemplateChainNameFromClusterDeployment(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterDeployment object", got)
	}
}

func TestExtractCredentialNameFromClusterDeployment(t *testing.T) {
	if got := ExtractCredentialNameFromClusterDeployment(&ClusterDeployment{Spec: ClusterDeploymentSpec{Credential: "cred1"}}); !reflect.DeepEqual(got, []string{"cred1"}) {
		t.Errorf("got %v, want [cred1]", got)
	}
	if got := ExtractCredentialNameFromClusterDeployment(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterDeployment object", got)
	}
}

func TestExtractClusterAuthenticationNameFromClusterDeployment(t *testing.T) {
	if got := ExtractClusterAuthenticationNameFromClusterDeployment(&ClusterDeployment{Spec: ClusterDeploymentSpec{ClusterAuth: "auth1"}}); !reflect.DeepEqual(got, []string{"auth1"}) {
		t.Errorf("got %v, want [auth1]", got)
	}
	if got := ExtractClusterAuthenticationNameFromClusterDeployment(&ClusterDeployment{}); got != nil {
		t.Errorf("got %v, want nil for empty ClusterAuth", got)
	}
	if got := ExtractClusterAuthenticationNameFromClusterDeployment(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterDeployment object", got)
	}
}

func TestExtractClusterAuditPolicyNameFromClusterDeployment(t *testing.T) {
	if got := ExtractClusterAuditPolicyNameFromClusterDeployment(&ClusterDeployment{Spec: ClusterDeploymentSpec{AuditPolicy: "policy1"}}); !reflect.DeepEqual(got, []string{"policy1"}) {
		t.Errorf("got %v, want [policy1]", got)
	}
	if got := ExtractClusterAuditPolicyNameFromClusterDeployment(&ClusterDeployment{}); got != nil {
		t.Errorf("got %v, want nil for empty AuditPolicy", got)
	}
	if got := ExtractClusterAuditPolicyNameFromClusterDeployment(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterDeployment object", got)
	}
}

func TestExtractReleaseVersion(t *testing.T) {
	if got := extractReleaseVersion(&Release{Spec: ReleaseSpec{Version: "1.2.3"}}); !reflect.DeepEqual(got, []string{"1.2.3"}) {
		t.Errorf("got %v, want [1.2.3]", got)
	}
	if got := extractReleaseVersion(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-Release object", got)
	}
}

func TestExtractReleaseTemplates(t *testing.T) {
	release := &Release{Spec: ReleaseSpec{
		KCM:  CoreProviderTemplate{Template: "kcm-tpl"},
		CAPI: CoreProviderTemplate{Template: "capi-tpl"},
	}}
	got := extractReleaseTemplates(release)
	if len(got) == 0 {
		t.Errorf("got %v, want non-empty (matches release.Templates())", got)
	}
	if !reflect.DeepEqual(got, release.Templates()) {
		t.Errorf("got %v, want %v (release.Templates())", got, release.Templates())
	}
	if got := extractReleaseTemplates(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-Release object", got)
	}
}

func TestExtractSupportedTemplatesNames(t *testing.T) {
	spec := TemplateChainSpec{SupportedTemplates: []SupportedTemplate{{Name: "t1"}, {Name: "t2"}}}

	if got := extractSupportedTemplatesNames(&ClusterTemplateChain{Spec: spec}); !reflect.DeepEqual(got, []string{"t1", "t2"}) {
		t.Errorf("got %v, want [t1 t2] for ClusterTemplateChain", got)
	}
	if got := extractSupportedTemplatesNames(&ServiceTemplateChain{Spec: spec}); !reflect.DeepEqual(got, []string{"t1", "t2"}) {
		t.Errorf("got %v, want [t1 t2] for ServiceTemplateChain", got)
	}
	if got := extractSupportedTemplatesNames(&Region{}); got != nil {
		t.Errorf("got %v, want nil for unrelated object", got)
	}
}

func TestExtractProvidersFromClusterTemplate(t *testing.T) {
	ct := &ClusterTemplate{Status: ClusterTemplateStatus{Providers: Providers{"aws", "azure"}}}
	if got := ExtractProvidersFromClusterTemplate(ct); !reflect.DeepEqual(got, []string{"aws", "azure"}) {
		t.Errorf("got %v, want [aws azure]", got)
	}
	if got := ExtractProvidersFromClusterTemplate(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ClusterTemplate object", got)
	}
}

func TestExtractServiceTemplateNamesFromMultiClusterService(t *testing.T) {
	mcs := &MultiClusterService{Spec: MultiClusterServiceSpec{ServiceSpec: ServiceSpec{
		Services: []Service{{Template: "svc1"}, {Template: "svc2"}},
	}}}
	if got := ExtractServiceTemplateNamesFromMultiClusterService(mcs); !reflect.DeepEqual(got, []string{"svc1", "svc2"}) {
		t.Errorf("got %v, want [svc1 svc2]", got)
	}
	if got := ExtractServiceTemplateNamesFromMultiClusterService(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-MultiClusterService object", got)
	}
}

func TestExtractServiceTemplateChainNamesFromMultiClusterService(t *testing.T) {
	mcs := &MultiClusterService{Spec: MultiClusterServiceSpec{ServiceSpec: ServiceSpec{
		Services: []Service{{Template: "svc1", TemplateChain: "chain1"}, {Template: "svc2"}},
	}}}
	if got := ExtractServiceTemplateChainNamesFromMultiClusterService(mcs); !reflect.DeepEqual(got, []string{"chain1"}) {
		t.Errorf("got %v, want [chain1]", got)
	}
	if got := ExtractServiceTemplateChainNamesFromMultiClusterService(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-MultiClusterService object", got)
	}
}

func TestExtractOwnerReferences(t *testing.T) {
	obj := &ProviderTemplate{ObjectMeta: metav1.ObjectMeta{
		OwnerReferences: []metav1.OwnerReference{{Name: "owner1"}, {Name: "owner2"}},
	}}
	if got := extractOwnerReferences(obj); !reflect.DeepEqual(got, []string{"owner1", "owner2"}) {
		t.Errorf("got %v, want [owner1 owner2]", got)
	}
	if got := extractOwnerReferences(&ProviderTemplate{}); len(got) != 0 {
		t.Errorf("got %v, want empty for no owner references", got)
	}
}

func TestExtractScheduledOrIncompleteBackups(t *testing.T) {
	t.Run("scheduled backup: true", func(t *testing.T) {
		mb := &ManagementBackup{Spec: ManagementBackupSpec{Schedule: "@daily"}}
		if got := ExtractScheduledOrIncompleteBackups(mb); !reflect.DeepEqual(got, []string{"true"}) {
			t.Errorf("got %v, want [true]", got)
		}
	})

	t.Run("unscheduled and incomplete backup: true", func(t *testing.T) {
		mb := &ManagementBackup{}
		if got := ExtractScheduledOrIncompleteBackups(mb); !reflect.DeepEqual(got, []string{"true"}) {
			t.Errorf("got %v, want [true] (not completed)", got)
		}
	})

	t.Run("unscheduled and completed backup: nil", func(t *testing.T) {
		ts := metav1.Now()
		mb := &ManagementBackup{Status: ManagementBackupStatus{
			ManagementBackupSingleStatus: ManagementBackupSingleStatus{
				LastBackup: &velerov1.BackupStatus{CompletionTimestamp: &ts},
			},
		}}
		if got := ExtractScheduledOrIncompleteBackups(mb); got != nil {
			t.Errorf("got %v, want nil (completed, not scheduled)", got)
		}
	})

	t.Run("non-ManagementBackup object: nil", func(t *testing.T) {
		if got := ExtractScheduledOrIncompleteBackups(&Region{}); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestExtractServiceSetCluster(t *testing.T) {
	ss := &ServiceSet{Spec: ServiceSetSpec{Cluster: "cluster1"}}
	if got := ExtractServiceSetCluster(ss); !reflect.DeepEqual(got, []string{"cluster1"}) {
		t.Errorf("got %v, want [cluster1]", got)
	}
	if got := ExtractServiceSetCluster(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ServiceSet object", got)
	}
}

func TestExtractServiceSetMultiClusterService(t *testing.T) {
	ss := &ServiceSet{Spec: ServiceSetSpec{MultiClusterService: "mcs1"}}
	if got := ExtractServiceSetMultiClusterService(ss); !reflect.DeepEqual(got, []string{"mcs1"}) {
		t.Errorf("got %v, want [mcs1]", got)
	}
	if got := ExtractServiceSetMultiClusterService(&ServiceSet{}); got != nil {
		t.Errorf("got %v, want nil for empty MultiClusterService", got)
	}
	if got := ExtractServiceSetMultiClusterService(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ServiceSet object", got)
	}
}

func TestExtractServiceSetProvider(t *testing.T) {
	ss := &ServiceSet{Spec: ServiceSetSpec{Provider: StateManagementProviderConfig{Name: "provider1"}}}
	if got := ExtractServiceSetProvider(ss); !reflect.DeepEqual(got, []string{"provider1"}) {
		t.Errorf("got %v, want [provider1]", got)
	}
	if got := ExtractServiceSetProvider(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-ServiceSet object", got)
	}
}

func TestExtractCredentialRegion(t *testing.T) {
	cred := &Credential{Spec: CredentialSpec{Region: "region1"}}
	if got := ExtractCredentialRegion(cred); !reflect.DeepEqual(got, []string{"region1"}) {
		t.Errorf("got %v, want [region1]", got)
	}
	if got := ExtractCredentialRegion(&Region{}); got != nil {
		t.Errorf("got %v, want nil for non-Credential object", got)
	}
}
