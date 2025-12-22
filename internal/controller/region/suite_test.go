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

package region

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var (
	ctx        context.Context
	cancel     context.CancelFunc
	mgmtEnv    *envtest.Environment
	mgmtClient client.Client
	config     *rest.Config

	rgnEnv        *envtest.Environment
	rgnClient     client.Client
	rgnKubeconfig []byte
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Region Integration")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO()) //nolint:fatcontext
	mgmtEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "templates", "provider", "kcm", "templates", "crds"),
			filepath.Join("..", "..", "..", "templates", "provider", "kcm-regional", "templates", "crds"),
			filepath.Join("..", "..", "..", "bin", "crd"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.33.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}
	var err error
	config, err = mgmtEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(config).NotTo(BeNil())

	Expect(kcmv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(sourcev1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(helmcontrollerv2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(addoncontrollerv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(libsveltosv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(clusterapiv1.AddToScheme(scheme.Scheme)).To(Succeed())

	mgmtClient, err = client.New(config, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(mgmtClient).NotTo(BeNil())

	rgnEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "templates", "provider", "kcm-regional", "templates", "crds"),
			filepath.Join("..", "..", "..", "bin", "crd"),
		},
	}

	rgnClient, rgnKubeconfig = getRegionalClient()

	komega.SetClient(mgmtClient)
	komega.SetContext(ctx)
})

var _ = AfterSuite(func() {
	Expect(rgnEnv.Stop()).NotTo(HaveOccurred())
	Expect(mgmtEnv.Stop()).NotTo(HaveOccurred())
	cancel()
})

func getRegionalClient() (client.Client, []byte) {
	var err error
	cfg, err := rgnEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	clusterName := "rgn"
	userName := "rgn-user"
	contextName := "rgn-context"

	kc := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			clusterName: {
				Server:                   cfg.Host,
				CertificateAuthorityData: cfg.CAData,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			userName: {
				ClientCertificateData: cfg.CertData,
				ClientKeyData:         cfg.KeyData,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: {
				Cluster:  clusterName,
				AuthInfo: userName,
			},
		},
		CurrentContext: contextName,
	}

	kubeconfig, err := clientcmd.Write(kc)
	Expect(err).NotTo(HaveOccurred())

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	Expect(err).NotTo(HaveOccurred())

	cl, err := client.New(restConfig, client.Options{})
	Expect(err).NotTo(HaveOccurred())

	return cl, kubeconfig
}
