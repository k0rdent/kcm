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
	"context"
	_ "embed"
	"fmt"
	"slices"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

type TestingProvider string

const (
	TestingProviderAWS     TestingProvider = "aws"
	TestingProviderAzure   TestingProvider = "azure"
	TestingProviderGCP     TestingProvider = "gcp"
	TestingProviderVsphere TestingProvider = "vsphere"
	TestingProviderAdopted TestingProvider = "adopted"
	TestingProviderRemote  TestingProvider = "remote"
)

var (
	//go:embed config.yaml
	configBytes []byte

	Config TestingConfig

	parseOnce sync.Once
	errParse  error
)

type TestingConfig = map[TestingProvider][]ProviderTestingConfig

type ProviderTestingConfig struct {
	// ClusterTestingConfig contains the testing configuration for the cluster deployment.
	ClusterTestingConfig `yaml:",inline"`
	// Hosted contains the testing configuration for the hosted cluster deployment using the previously deployed
	// cluster as a management. If omitted, the hosted cluster deployment will be skipped.
	Hosted *ClusterTestingConfig `yaml:"hosted,omitempty"`
}

type ClusterTestingConfig struct {
	// Upgrade is a boolean parameter that specifies whether the cluster deployment upgrade should be tested.
	Upgrade bool `yaml:"upgrade,omitempty"`
	// Template is the name of the template to use when creating a cluster deployment.
	// If unset:
	// * The latest available template will be chosen
	// * If upgrade is triggered, the latest available template with available upgrades will be chosen.
	Template string `yaml:"template,omitempty"`
	// UpgradeTemplate specifies the name of the template to upgrade to. Ignored if upgrade is set to false.
	// If unset, the latest template available for the upgrade will be chosen.
	UpgradeTemplate string `yaml:"upgradeTemplate,omitempty"`
}

func Parse() error {
	parseOnce.Do(func() {
		err := yaml.Unmarshal(configBytes, &Config)
		if err != nil {
			errParse = fmt.Errorf("failed to decode base64 configuration: %w", err)
			return
		}
	})
	return errParse
}

func Show() string {
	prettyConfig, err := yaml.Marshal(Config)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}

func UpgradeRequired() bool {
	for _, configs := range Config {
		for _, config := range configs {
			if config.Upgrade {
				return true
			}
		}
	}
	return false
}

func SetDefaults(ctx context.Context, cl crclient.Client) {
	clusterTemplates, err := templates.GetSortedClusterTemplates(ctx, cl, internalutils.DefaultSystemNamespace)
	Expect(err).NotTo(HaveOccurred())

	_, _ = fmt.Fprintf(GinkgoWriter, "Found ClusterTemplates:\n%v\n", clusterTemplates)

	if len(Config) == 0 {
		Config = map[TestingProvider][]ProviderTestingConfig{
			TestingProviderAWS:     {},
			TestingProviderAzure:   {},
			TestingProviderGCP:     {},
			TestingProviderVsphere: {},
			TestingProviderAdopted: {},
			TestingProviderRemote:  {},
		}
	}
	for provider, configs := range Config {
		if len(configs) == 0 {
			Config[provider] = getDefaultTestingConfiguration()
		}
		for i := range Config[provider] {
			c := Config[provider][i]
			err := c.SetTemplates(clusterTemplates, getTemplateType(provider))
			Expect(err).NotTo(HaveOccurred())

			if c.Hosted != nil {
				err = c.Hosted.SetTemplates(clusterTemplates, getHostedTemplateType(provider))
				Expect(err).NotTo(HaveOccurred())
			}
			Config[provider][i] = c
		}
	}
}

func (c *ProviderTestingConfig) String() string {
	prettyConfig, err := yaml.Marshal(c)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}

func (c *ClusterTestingConfig) SetTemplates(clusterTemplates []string, templateType templates.Type) error {
	if c.Template != "" && !slices.Contains(clusterTemplates, c.Template) {
		return fmt.Errorf("the ClusterTemplate %s does not exist", c.Template)
	}
	if c.UpgradeTemplate != "" && !slices.Contains(clusterTemplates, c.UpgradeTemplate) {
		return fmt.Errorf("the ClusterTemplate %s does not exist", c.UpgradeTemplate)
	}
	tmpls := templates.FindLatestTemplatesWithType(clusterTemplates, templateType, 2)
	if !c.Upgrade {
		if c.Template == "" {
			if len(tmpls) == 0 {
				return fmt.Errorf("no Template of the %s type was found", templateType)
			}
			c.Template = tmpls[0]
			return nil
		}
		return nil
	}
	if len(tmpls) < 2 {
		return fmt.Errorf("could not find 2 templates with %s type to test upgrade. Found templates: %+v", templateType, tmpls)
	}
	c.Template = tmpls[1]
	c.UpgradeTemplate = tmpls[0]
	return nil
}
