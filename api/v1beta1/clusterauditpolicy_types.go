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

const (
	auditPolicyAPIVersion = "audit.k8s.io/v1"
	auditPolicyKind       = "Policy"
)

// ClusterAuditPolicySpec defines the desired state of ClusterAuditPolicy
type ClusterAuditPolicySpec struct {
	// Policy contains the full content of a kubernetes [Policy] object used to configure auditing.
	Policy Policy `json:"policy"`
}

// Policy defines the structure of the kubernetes Policy object used to configure auditing.
//
// This type is derived from the upstream Kubernetes implementation of [k8s.io/apiserver/pkg/apis/audit/v1.Policy]
type Policy struct {
	// Rules specify the audit Level a request should be recorded at.
	// A request may match multiple rules, in which case the FIRST matching rule is used.
	// The default audit level is None, but can be overridden by a catch-all rule at the end of the list.
	// PolicyRules are strictly ordered.
	// +listType=atomic
	Rules []auditv1.PolicyRule `json:"rules" protobuf:"bytes,2,rep,name=rules"`

	// OmitStages is a list of stages for which no events are created. Note that this can also
	// be specified per rule in which case the union of both are omitted.
	// +optional
	// +listType=atomic
	OmitStages []auditv1.Stage `json:"omitStages,omitempty" protobuf:"bytes,3,rep,name=omitStages"`

	// OmitManagedFields indicates whether to omit the managed fields of the request
	// and response bodies from being written to the API audit log.
	// This is used as a global default - a value of 'true' will omit the managed fileds,
	// otherwise the managed fields will be included in the API audit log.
	// Note that this can also be specified per rule in which case the value specified
	// in a rule will override the global default.
	// +optional
	OmitManagedFields bool `json:"omitManagedFields,omitempty" protobuf:"varint,4,opt,name=omitManagedFields"`
}

// +kubebuilder:object:root=true

// ClusterAuditPolicy is the Schema for the clusterauditpolicies API
type ClusterAuditPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec ClusterAuditPolicySpec `json:"spec"`
}

// GetPolicy builds the auditv1.Policy object from the ClusterAuditPolicySpec.
func (s *ClusterAuditPolicySpec) GetPolicy() *auditv1.Policy {
	if s == nil {
		return &auditv1.Policy{}
	}

	return &auditv1.Policy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: auditPolicyAPIVersion,
			Kind:       auditPolicyKind,
		},
		Rules:             s.Policy.Rules,
		OmitStages:        s.Policy.OmitStages,
		OmitManagedFields: s.Policy.OmitManagedFields,
	}
}

// ToAuditPolicy converts the auditv1.Policy object to audit.Policy for further validation
func (p *ClusterAuditPolicy) ToAuditPolicy() (*audit.Policy, error) {
	if p == nil {
		return &audit.Policy{}, nil
	}

	outBytes, err := json.Marshal(p.Spec.GetPolicy())
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
