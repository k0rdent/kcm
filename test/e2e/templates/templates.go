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

package templates

import (
	"strings"
)

type Type string

const (
	TemplateAWSStandaloneCP     Type = "aws-standalone-cp"
	TemplateAWSHostedCP         Type = "aws-hosted-cp"
	TemplateAWSEKS              Type = "aws-eks"
	TemplateAzureStandaloneCP   Type = "azure-standalone-cp"
	TemplateAzureHostedCP       Type = "azure-hosted-cp"
	TemplateVSphereStandaloneCP Type = "vsphere-standalone-cp"
	TemplateVSphereHostedCP     Type = "vsphere-hosted-cp"
	TemplateAdoptedCluster      Type = "adopted-cluster"
)

// Default is a map where each key represents a supported template type,
// and the corresponding value is the default template name for that type.
var Default = map[Type]string{
	TemplateAWSStandaloneCP:     "aws-standalone-cp-0-1-0",
	TemplateAWSHostedCP:         "aws-hosted-cp-0-1-0",
	TemplateAWSEKS:              "aws-eks-0-1-0",
	TemplateAzureStandaloneCP:   "azure-standalone-cp-0-1-0",
	TemplateAzureHostedCP:       "azure-hosted-cp-0-1-0",
	TemplateVSphereStandaloneCP: "vsphere-standalone-cp-0-1-0",
	TemplateVSphereHostedCP:     "vsphere-hosted-cp-0-1-0",
	TemplateAdoptedCluster:      "adopted-cluster-0-1-0",
}

func GetType(template string) Type {
	for t := range Default {
		if strings.HasPrefix(template, string(t)) {
			return t
		}
	}
	return ""
}
