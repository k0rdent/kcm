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
	"errors"
	"fmt"
	texttemplate "text/template"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type (
	cloudConfFields struct {
		AuthURL                     string
		ApplicationCredentialID     string
		ApplicationCredentialName   string
		ApplicationCredentialSecret string
		Username                    string
		Password                    string
		RegionName                  string
		FloatingNetworkID           string
		PublicNetworkName           string
	}

	cloudsYaml struct {
		Clouds map[string]cloud `yaml:"clouds"`
	}

	cloud struct {
		Auth       auth   `yaml:"auth"`
		RegionName string `yaml:"region_name"`
	}

	auth struct {
		AuthURL                     string `yaml:"auth_url"`
		ApplicationCredentialID     string `yaml:"application_credential_id"`
		ApplicationCredentialName   string `yaml:"application_credential_name"`
		ApplicationCredentialSecret string `yaml:"application_credential_secret"`
		Username                    string `yaml:"username"`
		Password                    string `yaml:"password"`
		ProjectDomainName           string `yaml:"project_domain_name"`
	}
)

// PropagateOpenStackProviderObjects propagates OpenStack secrets
func PropagateOpenStackProviderObjects(ctx context.Context, cfg *PropagationCfg) error {
	if cfg == nil {
		return errors.New("PropagationCfg is nil")
	}
	// Fetch the OpenStackCluster resource
	openstackCluster := &capo.OpenStackCluster{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      cfg.ClusterDeployment.Name,
		Namespace: cfg.ClusterDeployment.Namespace,
	}, openstackCluster); err != nil {
		return fmt.Errorf("unable to get OpenStackCluster %s/%s: %w",
			cfg.ClusterDeployment.Namespace, cfg.ClusterDeployment.Name, err)
	}

	// Fetch the OpenStack secret
	openstackSecret, err := fetchOpenStackSecret(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to fetch OpenStack secret: %w", err)
	}

	// Generate the CCM secret using the extracted cloudName
	ccmSecret, err := generateOpenStackCCMSecret(openstackCluster, openstackSecret)
	if err != nil {
		return fmt.Errorf("failed to generate CCM secret: %w", err)
	}

	// Apply the CCM configuration
	if err := applyCCMConfigs(ctx, cfg.KubeconfSecret, ccmSecret); err != nil {
		return fmt.Errorf("failed to apply CCM configuration: %w", err)
	}

	return nil
}

// Fetch the OpenStack secret
func fetchOpenStackSecret(ctx context.Context, cfg *PropagationCfg) (*corev1.Secret, error) {
	openstackSecret := &corev1.Secret{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      cfg.IdentityRef.Name,
		Namespace: cfg.IdentityRef.Namespace,
	}, openstackSecret); err != nil {
		return nil, fmt.Errorf("failed to get OpenStack secret %s/%s: %w",
			cfg.IdentityRef.Namespace, cfg.IdentityRef.Name, err)
	}
	return openstackSecret, nil
}

// Generate the CCM secret from the OpenStack secret
func generateOpenStackCCMSecret(openstackCluster *capo.OpenStackCluster, openstackSecret *corev1.Secret) (*corev1.Secret, error) {
	const cloudConfTemplate = `
[Global]
auth-url="{{ .AuthURL }}"
{{- if .ApplicationCredentialID }}
application-credential-id="{{ .ApplicationCredentialID }}"
{{- end }}
{{- if .ApplicationCredentialName }}
application-credential-name="{{ .ApplicationCredentialName }}"
{{- end }}
{{- if .ApplicationCredentialSecret }}
application-credential-secret="{{ .ApplicationCredentialSecret }}"
{{- end }}
{{- if and (not .ApplicationCredentialID) (not .ApplicationCredentialSecret) }}
username="{{ .Username }}"
password="{{ .Password }}"
{{- end }}
region="{{ .RegionName }}"

[LoadBalancer]
{{- if .FloatingNetworkID }}
floating-network-id="{{ .FloatingNetworkID }}"
{{- end }}

[Network]
{{- if .PublicNetworkName }}
public-network-name="{{ .PublicNetworkName }}"
{{- end }}
`

	// Parse the clouds.yaml content
	cloudsYamlData, ok := openstackSecret.Data["clouds.yaml"]
	if !ok {
		return nil, errors.New("missing clouds.yaml in OpenStack secret")
	}

	parsedCloudsYaml, err := parseCloudsYaml(cloudsYamlData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse clouds.yaml: %w", err)
	}

	// Extract cloudConfFields using the provided cloudName
	fields, err := extractCloudConfFields(parsedCloudsYaml, openstackCluster.Spec.IdentityRef.CloudName)
	if err != nil {
		return nil, fmt.Errorf("failed to extract cloud.conf fields: %w", err)
	}

	// Fetch external network details from OpenStackCluster
	// externalNetwork, err := fetchExternalNetwork(ctx, cfg)
	externalNetwork := openstackCluster.Status.ExternalNetwork
	if externalNetwork == nil || externalNetwork.ID == "" || externalNetwork.Name == "" {
		return nil, errors.New("external network details are incomplete")
	}
	fields.FloatingNetworkID = externalNetwork.ID
	fields.PublicNetworkName = externalNetwork.Name

	// Render the cloud.conf secret
	return renderCloudConf(cloudConfTemplate, fields)
}

// Parse the clouds.yaml content into structured types
func parseCloudsYaml(data []byte) (*cloudsYaml, error) {
	var parsed cloudsYaml
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse clouds.yaml: %w", err)
	}
	return &parsed, nil
}

// Extract fields required for the cloud.conf file
func extractCloudConfFields(cy *cloudsYaml, cloudName string) (cloudConfFields, error) {
	var fields cloudConfFields

	cloud, exists := cy.Clouds[cloudName]
	if !exists {
		return fields, fmt.Errorf("cloud '%s' not found in clouds.yaml", cloudName)
	}

	auth := cloud.Auth
	fields = cloudConfFields{
		AuthURL:                     auth.AuthURL,
		ApplicationCredentialID:     auth.ApplicationCredentialID,
		ApplicationCredentialName:   auth.ApplicationCredentialName,
		ApplicationCredentialSecret: auth.ApplicationCredentialSecret,
		Username:                    auth.Username,
		Password:                    auth.Password,
		RegionName:                  cloud.RegionName,
	}

	return fields, nil
}

// Render cloud.conf using the template and fields
func renderCloudConf(templateStr string, fields cloudConfFields) (*corev1.Secret, error) {
	tmpl, err := texttemplate.New("cloudConf").Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cloud.conf template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, fields); err != nil {
		return nil, fmt.Errorf("failed to render cloud.conf template: %w", err)
	}

	secretData := map[string][]byte{
		"cloud.conf": buf.Bytes(),
	}

	return makeSecret("openstack-cloud-config", secretData), nil
}
