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

package audit

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	"sigs.k8s.io/yaml"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var knownAPIs = []string{
	"",
	"admissionregistration.k8s.io",
	"apiextensions.k8s.io",
	"apiregistration.k8s.io",
	"apps",
	"authentication.k8s.io",
	"authorization.k8s.io",
	"autoscaling",
	"batch",
	"certificates.k8s.io",
	"coordination.k8s.io",
	"discovery.k8s.io",
	"extensions",
	"flowcontrol.apiserver.k8s.io",
	"metrics.k8s.io",
	"networking.k8s.io",
	"node.k8s.io",
	"policy",
	"rbac.authorization.k8s.io",
	"scheduling.k8s.io",
	"storage.k8s.io",
	// k0s
	"k0s.k0sproject.io",
	"helm.k0sproject.io",
	"autopilot.k0sproject.io",
	// CAPI
	"cluster.x-k8s.io",
	"infrastructure.cluster.x-k8s.io",
	"controlplane.cluster.x-k8s.io",
	"bootstrap.cluster.x-k8s.io",
	"addons.cluster.x-k8s.io",
	"ipam.cluster.x-k8s.io",
	// Flux
	"source.toolkit.fluxcd.io",
	"helm.toolkit.fluxcd.io",
	"kustomize.toolkit.fluxcd.io",
	// Sveltos
	"lib.projectsveltos.io",
	"config.projectsveltos.io",
}

//go:embed default_policy.yaml.tpl
var defaultPolicy []byte

func GetDefaultClusterAuditPolicySpec() (kcmv1.ClusterAuditPolicySpec, error) {
	policy, err := GetDefaultPolicy()
	if err != nil {
		return kcmv1.ClusterAuditPolicySpec{}, fmt.Errorf("failed to get default audit policy: %w", err)
	}

	return kcmv1.ClusterAuditPolicySpec{
		Policy: policy,
	}, nil
}

func GetDefaultPolicy() (auditv1.Policy, error) {
	tpl, err := template.New("policy").Parse(string(defaultPolicy))
	if err != nil {
		return auditv1.Policy{}, fmt.Errorf("failed to parse audit policy template: %w", err)
	}

	data := struct {
		KnownAPIs []string
	}{
		KnownAPIs: knownAPIs,
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return auditv1.Policy{}, fmt.Errorf("failed to execute audit policy template: %w", err)
	}

	policy := auditv1.Policy{}
	if err = yaml.Unmarshal(buf.Bytes(), &policy); err != nil {
		return auditv1.Policy{}, fmt.Errorf("failed to unmarshal audit policy: %w", err)
	}

	return policy, nil
}
