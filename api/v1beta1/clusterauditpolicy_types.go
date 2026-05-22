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
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/apis/audit"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

// ClusterAuditPolicySpec defines the desired state of ClusterAuditPolicy
type ClusterAuditPolicySpec struct {
	// Policy defines the configuration of audit logging, and the rules for how different request
	// categories are logged.
	//
	// For more details, see: https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/
	Policy auditv1.Policy `json:"policy"`
}

// +kubebuilder:object:root=true

// ClusterAuditPolicy is the Schema for the clusterauditpolicies API
type ClusterAuditPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec ClusterAuditPolicySpec `json:"spec,omitempty"`
}

// ToAuditPolicy converts the auditv1.Policy object to audit.Policy for further validation
func (p *ClusterAuditPolicy) ToAuditPolicy() (*audit.Policy, error) {
	if p == nil {
		return &audit.Policy{}, nil
	}

	outBytes, err := json.Marshal(p.Spec.Policy)
	if err != nil {
		return nil, fmt.Errorf("error marshaling audit policy to JSON: %w", err)
	}

	auditPolicy := &audit.Policy{}
	if err := json.Unmarshal(outBytes, auditPolicy); err != nil {
		return nil, fmt.Errorf("error unmarshalling audit v1.Policy JSON to audit.Policy: %w", err)
	}

	return auditPolicy, nil
}

// +kubebuilder:object:root=true

// ClusterAuditPolicyList contains a list of ClusterAuditPolicy
type ClusterAuditPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAuditPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterAuditPolicy{}, &ClusterAuditPolicyList{})
}
