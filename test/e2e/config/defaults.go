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

package config

import (
	"github.com/K0rdent/kcm/test/e2e/templates"
)

func getDefaultTestingConfiguration(provider TestingProvider) []ProviderTestingConfig {
	switch provider {
	case TestingProviderAWS:
		return []ProviderTestingConfig{
			newTestingCluster(templates.TemplateAWSStandaloneCP, templates.TemplateAWSHostedCP),
			newTestingCluster(templates.TemplateAWSEKS, ""),
		}
	case TestingProviderAzure:
		return []ProviderTestingConfig{newTestingCluster(templates.TemplateAzureStandaloneCP, templates.TemplateAzureHostedCP)}
	case TestingProviderVsphere:
		return []ProviderTestingConfig{newTestingCluster(templates.TemplateVSphereStandaloneCP, templates.TemplateVSphereHostedCP)}
	case TestingProviderAdopted:
		return []ProviderTestingConfig{newTestingCluster(templates.TemplateAdoptedCluster, "")}
	default:
		return nil
	}
}

func newTestingCluster(templateType, hostedTemplateType templates.Type) ProviderTestingConfig {
	config := ProviderTestingConfig{
		ClusterTestingConfig: ClusterTestingConfig{
			Template: templates.Default[templateType],
		},
	}
	if hostedTemplateType != "" {
		config.Hosted = &ClusterTestingConfig{
			Template: templates.Default[hostedTemplateType],
		}
	}
	return config
}

func getDefaultTemplate(provider TestingProvider) string {
	switch provider {
	case TestingProviderAWS:
		return templates.Default[templates.TemplateAWSStandaloneCP]
	case TestingProviderAzure:
		return templates.Default[templates.TemplateAzureStandaloneCP]
	case TestingProviderVsphere:
		return templates.Default[templates.TemplateVSphereStandaloneCP]
	case TestingProviderAdopted:
		return templates.Default[templates.TemplateAdoptedCluster]
	default:
		return ""
	}
}

func getDefaultHostedTemplate(provider TestingProvider) string {
	switch provider {
	case TestingProviderAWS:
		return templates.Default[templates.TemplateAWSHostedCP]
	case TestingProviderAzure:
		return templates.Default[templates.TemplateAzureHostedCP]
	case TestingProviderVsphere:
		return templates.Default[templates.TemplateVSphereHostedCP]
	default:
		return ""
	}
}
