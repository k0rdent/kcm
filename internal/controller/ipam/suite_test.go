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

package ipam

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/client-go/kubernetes/scheme"
	inclusteripamv1alpha2 "sigs.k8s.io/cluster-api-ipam-provider-in-cluster/api/v1alpha2"
	capioperator "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	pollingInterval   = 30 * time.Millisecond
	eventuallyTimeout = 3 * time.Second
)

var (
	k8sClient client.Client
	mgrClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestControllers(t *testing.T) {
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")

	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "templates", "provider", "kcm", "templates", "crds"),
			filepath.Join("..", "..", "..", "bin", "crd"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.32.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	// cfg is defined in this file globally.
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(kcmv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(sourcev1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(sourcev1beta2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(helmcontrollerv2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(sveltosv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(capioperator.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(clusterapiv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(velerov1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(libsveltosv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(ipamv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(inclusteripamv1alpha2.AddToScheme(scheme.Scheme)).To(Succeed())
	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	SetClient(k8sClient)

	// start webhook server using Manager
	webhookInstallOptions := &testEnv.WebhookInstallOptions

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookInstallOptions.LocalServingHost,
			Port:    webhookInstallOptions.LocalServingPort,
			CertDir: webhookInstallOptions.LocalServingCertDir,
		}),
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())
	mgrClient = mgr.GetClient()
	Expect(mgrClient).NotTo(BeNil())

	err = kcmv1.SetupIndexers(ctx, mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
