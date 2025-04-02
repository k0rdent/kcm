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

func getDefaultTestingConfiguration() []ProviderTestingConfig {
	return []ProviderTestingConfig{{ClusterTestingConfig: ClusterTestingConfig{}}}
}

func getTemplateType(provider TestingProvider) templates.Type {
	switch provider {
	case TestingProviderAWS:
		return templates.TemplateAWSStandaloneCP
	case TestingProviderAzure:
		return templates.TemplateAzureStandaloneCP
	case TestingProviderGCP:
		return templates.TemplateGCPStandaloneCP
	case TestingProviderVsphere:
		return templates.TemplateVSphereStandaloneCP
	case TestingProviderAdopted:
		return templates.TemplateAdoptedCluster
	case TestingProviderRemote:
		return templates.TemplateRemoteCluster
	default:
		return ""
	}
}

func getHostedTemplateType(provider TestingProvider) templates.Type {
	switch provider {
	case TestingProviderAWS:
		return templates.TemplateAWSHostedCP
	case TestingProviderAzure:
		return templates.TemplateAzureHostedCP
	case TestingProviderGCP:
		return templates.TemplateGCPHostedCP
	case TestingProviderVsphere:
		return templates.TemplateVSphereHostedCP
	default:
		return ""
	}
}
