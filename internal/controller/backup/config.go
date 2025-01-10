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

package backup

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Config holds required set of parameters of to successfully install Velero stack and manipulate with it.
type Config struct {
	kubeRestConfig *rest.Config
	scheme         *runtime.Scheme
	cl             client.Client

	image           string
	systemNamespace string
	pluginImages    []string
	features        []string

	installRequeueAfter time.Duration
	objectsRequeueAfter time.Duration
}

// VeleroName contains velero name of different parts of the stack.
const VeleroName = "velero"

// ConfigOpt is the functional option for the Config.
type ConfigOpt func(*Config)

// NewConfig creates instance of the config.
func NewConfig(cl client.Client, kc *rest.Config, scheme *runtime.Scheme, opts ...ConfigOpt) *Config {
	c := newWithDefaults()

	for _, o := range opts {
		o(c)
	}

	c.cl = cl
	c.kubeRestConfig = kc
	c.scheme = scheme

	return c
}

// GetVeleroSystemNamespace returns the velero system namespace.
func (c *Config) GetVeleroSystemNamespace() string { return c.systemNamespace }

func (c *Config) getServiceAccountName() string {
	saName := VeleroName
	if c.systemNamespace != VeleroName {
		saName = VeleroName + "-" + c.systemNamespace + "-sa"
	}
	return saName
}

// WithInstallationRequeueAfter sets the RequeueAfter period if >0 for the Velero stack installation.
func WithInstallationRequeueAfter(d time.Duration) ConfigOpt {
	return func(c *Config) {
		if d == 0 {
			return
		}
		c.installRequeueAfter = d
	}
}

// WithObjectsRequeueAfter sets the RequeueAfter period if >0 for the reconciled objects.
func WithObjectsRequeueAfter(d time.Duration) ConfigOpt {
	return func(c *Config) {
		if d == 0 {
			return
		}
		c.objectsRequeueAfter = d
	}
}

// WithVeleroSystemNamespace sets the SystemNamespace if non-empty.
func WithVeleroSystemNamespace(ns string) ConfigOpt {
	return func(c *Config) {
		if len(ns) == 0 {
			return
		}
		c.systemNamespace = ns
	}
}

// WithPluginImages sets maps of plugins maintained by Velero.
func WithPluginImages(pluginImages ...string) ConfigOpt {
	return func(c *Config) {
		if len(pluginImages) == 0 {
			return
		}
		c.pluginImages = pluginImages
	}
}

// WithVeleroImage sets the main image for the Velero deployment if non-empty.
func WithVeleroImage(image string) ConfigOpt {
	return func(c *Config) {
		if len(image) == 0 {
			return
		}
		c.image = image
	}
}

// WithFeatures sets a list of features for the Velero deployment.
func WithFeatures(features ...string) ConfigOpt {
	return func(c *Config) {
		if len(features) == 0 {
			return
		}
		c.features = features
	}
}

func newWithDefaults() *Config {
	return &Config{
		installRequeueAfter: 5 * time.Second,
		objectsRequeueAfter: 5 * time.Minute,
		systemNamespace:     VeleroName,
		image:               fmt.Sprintf("%s/%s:%s", VeleroName, VeleroName, "v1.15.0"), // velero/velero:v1.15.0
		pluginImages: []string{
			"velero/velero-plugin-for-aws:v1.11.0",
			"velero/velero-plugin-for-microsoft-azure:v1.11.0",
			"velero/velero-plugin-for-gcp:v1.11.0",
		},
	}
}
