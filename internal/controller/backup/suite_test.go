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
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg           *rest.Config
	k8sClient     client.Client
	indexedClient client.Client
	testEnv       *envtest.Environment
	ctx           context.Context
	cancel        context.CancelFunc
)

const backupSystemNamespace = "test-backup-namespace"

const scheduleEvery6h = "@every 6h"

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Backup Controller Suite")
}

var mockRegionalClientFactory RegionalClientFactory

func setupMockClientFactory() {
	mockRegionalClientFactory = func(_ context.Context, mgmtClient client.Client, _ string, _ *kcmv1.Region, _ func() (*kruntime.Scheme, error)) (client.Client, *rest.Config, error) {
		return mgmtClient, nil, nil
	}
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO()) //nolint:fatcontext // on purpose

	By("bootstrapping test environment")
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
			fmt.Sprintf("1.33.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(kcmv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(velerov1.AddToScheme(scheme.Scheme)).To(Succeed())

	cache, err := cache.New(cfg, cache.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(cache).NotTo(BeNil())

	err = cache.IndexField(ctx, &kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment)
	Expect(err).NotTo(HaveOccurred())

	indexedClient, err = client.New(cfg, client.Options{
		Scheme: scheme.Scheme,
		Cache: &client.CacheOptions{
			Reader: cache,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(indexedClient).NotTo(BeNil())

	go func() {
		err := cache.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	Expect(cache.WaitForCacheSync(ctx)).To(BeTrue())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	By("Creating the backup system namespace")
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: backupSystemNamespace,
		},
	})).To(Succeed())

	Eventually(k8sClient.Get).
		WithArguments(ctx, client.ObjectKey{Name: backupSystemNamespace}, &corev1.Namespace{}).
		WithTimeout(10 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())

	setupMockClientFactory()
})

var _ = AfterSuite(func() {
	By("Deleting the backup system namespace")
	Expect(k8sClient.Delete(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: backupSystemNamespace,
		},
	})).To(Succeed())

	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
