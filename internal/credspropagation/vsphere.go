// Copyright 2024
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

package credspropagation

import (
	"bytes"
	"context"
	"fmt"
	texttemplate "text/template"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	capv "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

func PropagateVSphereSecrets(ctx context.Context, cfg *PropagationCfg) error {
	vsphereCluster := &capv.VSphereCluster{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      cfg.ClusterDeployment.Name,
		Namespace: cfg.ClusterDeployment.Namespace,
	}, vsphereCluster); err != nil {
		return fmt.Errorf("failed to get VSphereCluster %s: %w", cfg.ClusterDeployment.Name, err)
	}

	vsphereClIdty := &capv.VSphereClusterIdentity{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name: vsphereCluster.Spec.IdentityRef.Name,
	}, vsphereClIdty); err != nil {
		return fmt.Errorf("failed to get VSphereClusterIdentity %s: %w", vsphereCluster.Spec.IdentityRef.Name, err)
	}

	vsphereSecret := &corev1.Secret{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      vsphereClIdty.Spec.SecretName,
		Namespace: cfg.SystemNamespace,
	}, vsphereSecret); err != nil {
		return fmt.Errorf("failed to get VSphere Secret %s: %w", vsphereClIdty.Spec.SecretName, err)
	}

	vsphereMachines := &capv.VSphereMachineList{}
	if err := cfg.Client.List(
		ctx,
		vsphereMachines,
		&client.ListOptions{
			Namespace: cfg.ClusterDeployment.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				kcm.ClusterNameLabelKey: cfg.ClusterDeployment.Name,
			}),
			Limit: 1,
		},
	); err != nil {
		return fmt.Errorf("failed to list VSphereMachines for cluster %s: %w", cfg.ClusterDeployment.Name, err)
	}
	ccmSecret, ccmConfig, err := generateVSphereCCMConfigs(vsphereCluster, vsphereSecret, &vsphereMachines.Items[0])
	if err != nil {
		return fmt.Errorf("failed to generate VSphere CCM config: %w", err)
	}
	csiSecret, err := generateVSphereCSISecret(vsphereCluster, vsphereSecret, &vsphereMachines.Items[0])
	if err != nil {
		return fmt.Errorf("failed to generate VSphere CSI secret: %w", err)
	}

	if err := applyCCMConfigs(ctx, cfg.KubeconfSecret, ccmSecret, ccmConfig, csiSecret); err != nil {
		return fmt.Errorf("failed to apply VSphere CCM/CSI secrets: %w", err)
	}

	return nil
}

func generateVSphereCCMConfigs(vCl *capv.VSphereCluster, vScrt *corev1.Secret, vMa *capv.VSphereMachine) (*corev1.Secret, *corev1.ConfigMap, error) {
	const secretName = "vsphere-cloud-secret"
	secretData := map[string][]byte{
		vCl.Spec.Server + ".username": vScrt.Data["username"],
		vCl.Spec.Server + ".password": vScrt.Data["password"],
	}
	ccmCfg := map[string]any{
		"global": map[string]any{
			"port":            443,
			"insecureFlag":    true,
			"secretName":      secretName,
			"secretNamespace": metav1.NamespaceSystem,
		},
		"vcenter": map[string]any{
			vCl.Spec.Server: map[string]any{
				"server": vCl.Spec.Server,
				"datacenters": []string{
					vMa.Spec.Datacenter,
				},
			},
		},
		"labels": map[string]any{
			"region": "k8s-region",
			"zone":   "k8s-zone",
		},
	}

	ccmCfgYaml, err := yaml.Marshal(ccmCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal CCM config: %w", err)
	}

	cmData := map[string]string{
		"vsphere.conf": string(ccmCfgYaml),
	}
	return makeSecret(secretName, secretData),
		makeConfigMap("cloud-config", cmData),
		nil
}

func generateVSphereCSISecret(vCl *capv.VSphereCluster, vScrt *corev1.Secret, vMa *capv.VSphereMachine) (*corev1.Secret, error) {
	csiCfg := `
[Global]
cluster-id = "{{ .ClusterID }}"

[VirtualCenter "{{ .Vcenter }}"]
insecure-flag = "true"
user = "{{ .Username }}"
password = "{{ .Password }}"
port = "443"
datacenters = "{{ .Datacenter }}"
`
	type CSIFields struct {
		ClusterID, Vcenter, Username, Password, Datacenter string
	}

	fields := CSIFields{
		ClusterID:  vCl.Name,
		Vcenter:    vCl.Spec.Server,
		Username:   string(vScrt.Data["username"]),
		Password:   string(vScrt.Data["password"]),
		Datacenter: vMa.Spec.Datacenter,
	}

	tmpl, err := texttemplate.New("csiCfg").Parse(csiCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CSI secret (tmpl parse): %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, fields); err != nil {
		return nil, fmt.Errorf("failed to generate CSI secret (tmpl execute): %w", err)
	}

	secretData := map[string][]byte{
		"csi-vsphere.conf": buf.Bytes(),
	}

	return makeSecret("vcenter-config-secret", secretData), nil
}
