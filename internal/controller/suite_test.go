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

package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	capioperator "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	utilyaml "sigs.k8s.io/cluster-api/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils"
	kcmwebhook "github.com/K0rdent/kcm/internal/webhook"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	mutatingWebhookKind   = "MutatingWebhookConfiguration"
	validatingWebhookKind = "ValidatingWebhookConfiguration"
	testSystemNamespace   = "test-system-namespace"

	pollingInterval   = 30 * time.Millisecond
	eventuallyTimeout = 3 * time.Second
)

var (
	cfg           *rest.Config
	k8sClient     client.Client
	dynamicClient *dynamic.DynamicClient
	mgrClient     client.Client
	testEnv       *envtest.Environment
	ctx           context.Context
	cancel        context.CancelFunc
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

	ctx, cancel = context.WithCancel(context.TODO()) //nolint:fatcontext // on purpose

	_, mutatingWebhooks, err := loadWebhooks(
		filepath.Join("..", "..", "templates", "provider", "kcm", "templates", "webhooks.yaml"),
	)
	Expect(err).NotTo(HaveOccurred())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "templates", "provider", "kcm", "templates", "crds"),
			filepath.Join("..", "..", "bin", "crd"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.33.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			MutatingWebhooks: mutatingWebhooks,
		},
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(kcmv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(sourcev1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(helmcontrollerv2.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(addoncontrollerv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(capioperator.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(clusterapiv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(velerov1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(libsveltosv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	SetClient(k8sClient)

	dynamicClient, err = dynamic.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

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

	err = (&kcmwebhook.ClusterDeploymentValidator{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.MultiClusterServiceValidator{SystemNamespace: testSystemNamespace}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.ManagementValidator{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.AccessManagementValidator{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.ClusterTemplateChainValidator{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.ServiceTemplateChainValidator{}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	templateValidator := kcmwebhook.TemplateValidator{
		SystemNamespace: testSystemNamespace,
	}

	err = (&kcmwebhook.ClusterTemplateValidator{TemplateValidator: templateValidator}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.ServiceTemplateValidator{TemplateValidator: templateValidator}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&kcmwebhook.ProviderTemplateValidator{TemplateValidator: templateValidator}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// wait for the webhook server to get ready
	dialer := &net.Dialer{Timeout: time.Second}
	addrPort := fmt.Sprintf("%s:%d", webhookInstallOptions.LocalServingHost, webhookInstallOptions.LocalServingPort)
	Eventually(func() error {
		conn, err := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			return err
		}
		return conn.Close()
	}).Should(Succeed())

	Expect(seedClusterScopedResources(ctx, k8sClient)).To(Succeed())
	Expect(seedStateManagementProvider(ctx, k8sClient)).To(Succeed())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func loadWebhooks(path string) ([]*admissionv1.ValidatingWebhookConfiguration, []*admissionv1.MutatingWebhookConfiguration, error) {
	var validatingWebhooks []*admissionv1.ValidatingWebhookConfiguration
	var mutatingWebhooks []*admissionv1.MutatingWebhookConfiguration

	webhookFile, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	re := regexp.MustCompile("{{.*}}")
	s := re.ReplaceAllString(string(webhookFile), "")
	objs, err := utilyaml.ToUnstructured([]byte(s))
	if err != nil {
		return nil, nil, err
	}

	for i := range objs {
		o := objs[i]
		if o.GetKind() == validatingWebhookKind {
			o.SetName("validating-webhook")
			webhookConfig := &admissionv1.ValidatingWebhookConfiguration{}
			if err := scheme.Scheme.Convert(&o, webhookConfig, nil); err != nil {
				return nil, nil, err
			}
			validatingWebhooks = append(validatingWebhooks, webhookConfig)
		}

		if o.GetKind() == mutatingWebhookKind {
			o.SetName("mutating-webhook")
			webhookConfig := &admissionv1.MutatingWebhookConfiguration{}
			if err := scheme.Scheme.Convert(&o, webhookConfig, nil); err != nil {
				return nil, nil, err
			}
			mutatingWebhooks = append(mutatingWebhooks, webhookConfig)
		}
	}
	return validatingWebhooks, mutatingWebhooks, err
}

func seedClusterScopedResources(ctx context.Context, k8sClient client.Client) error {
	var (
		someProviderName     = "test-provider-name"
		otherProviderName    = "test-provider-name-other"
		someExposedContract  = "v1beta1_v1beta2"
		otherExposedContract = "v1beta1"
		capiVersion          = "v1beta1"
	)
	management := &kcmv1.Management{}

	By("creating the custom resource for the Kind Management")
	managementKey := client.ObjectKey{
		Name: kcmv1.ManagementName,
	}
	err := mgrClient.Get(ctx, managementKey, management)
	if errors.IsNotFound(err) {
		management = &kcmv1.Management{
			ObjectMeta: metav1.ObjectMeta{
				Name: kcmv1.ManagementName,
			},
			Spec: kcmv1.ManagementSpec{
				Release: "test-release",
			},
		}
		Expect(k8sClient.Create(ctx, management)).To(Succeed())
		management.Status = kcmv1.ManagementStatus{
			AvailableProviders: []string{someProviderName, otherProviderName},
			CAPIContracts:      map[string]kcmv1.CompatibilityContracts{someProviderName: {capiVersion: someExposedContract}, otherProviderName: {capiVersion: otherExposedContract}},
		}
		Expect(k8sClient.Status().Update(ctx, management)).To(Succeed())
	}
	return client.IgnoreNotFound(err)
}

func seedStateManagementProvider(ctx context.Context, k8sClient client.Client) error {
	var (
		smpName = utils.DefaultStateManagementProvider

		adapterAPIVersion = "sample-version/v1"
		adapterKind       = "SampleAdapter"
		adapterName       = "sample-adapter"
		adapterNamespace  = "sample-namespace"

		provisionerAPIVersion = "sample-version/v1"
		provisionerKind       = "SampleProvisioner"
		provisionerName       = "sample-provisioner"
		provisionerNamespace  = "sample-namespace"

		provisionerCRDGroup    = "sample-crd-group"
		provisionerCRDResource = "sample-crd-resources"
	)

	smp := &kcmv1.StateManagementProvider{}

	By("creating the custom resource for the Kind StateManagementProvider")
	smpKey := client.ObjectKey{
		Name: smpName,
	}
	err := mgrClient.Get(ctx, smpKey, smp)
	if errors.IsNotFound(err) {
		smp = &kcmv1.StateManagementProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name: smpName,
			},
			Spec: kcmv1.StateManagementProviderSpec{
				Selector: &metav1.LabelSelector{},
				Adapter: kcmv1.ResourceReference{
					APIVersion: adapterAPIVersion,
					Kind:       adapterKind,
					Name:       adapterName,
					Namespace:  adapterNamespace,
				},
				Provisioner: []kcmv1.ResourceReference{
					{
						APIVersion: provisionerAPIVersion,
						Kind:       provisionerKind,
						Name:       provisionerName,
						Namespace:  provisionerNamespace,
					},
				},
				ProvisionerCRDs: []kcmv1.ProvisionerCRD{
					{
						Group: provisionerCRDGroup,
						Resources: []string{
							provisionerCRDResource,
						},
					},
				},
				Suspend: false,
			},
		}
		Expect(k8sClient.Create(ctx, smp)).To(Succeed())
		smp.Status = kcmv1.StateManagementProviderStatus{
			Ready: true,
		}
		Expect(k8sClient.Status().Update(ctx, smp)).To(Succeed())
	}
	return client.IgnoreNotFound(err)
}
