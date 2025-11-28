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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/build"
	"github.com/K0rdent/kcm/internal/controller"
	"github.com/K0rdent/kcm/internal/controller/adapters/sveltos"
	"github.com/K0rdent/kcm/internal/controller/ipam"
	"github.com/K0rdent/kcm/internal/controller/region"
	"github.com/K0rdent/kcm/internal/controller/statemanagementprovider"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/telemetry"
	helmutil "github.com/K0rdent/kcm/internal/util/helm"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
	kcmwebhook "github.com/K0rdent/kcm/internal/webhook"
)

type config struct {
	kcmTemplatesChartName         string
	determinedRepositoryType      string
	registryCredentialsSecretName string
	registryCertSecretName        string
	globalRegistry                string
	globalK0sURL                  string
	k0sURLCertSecretName          string
	templatesRepoURL              string
	defaultHelmTimeout            time.Duration
	maxConcurrentReconciles       int
	insecureRegistry              bool
	createAccessManagement        bool
	enableWebhook                 bool
	createRelease                 bool
	createTemplates               bool
	enableSveltosCtrl             bool
	enableSveltosExpireCtrl       bool
	createManagement              bool
}

var (
	scheme   = schemeutil.MustGetManagementScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var (
		metricsAddr                   string
		probeAddr                     string
		secureMetrics                 bool
		enableHTTP2                   bool
		templatesRepoURL              string
		globalRegistry                string
		globalK0sURL                  string
		insecureRegistry              bool
		registryCredentialsSecretName string
		registryCertSecretName        string
		k0sURLCertSecretName          string
		createManagement              bool
		createAccessManagement        bool
		createRelease                 bool
		createTemplates               bool
		validateClusterUpgradePath    bool
		kcmTemplatesChartName         string
		enableWebhook                 bool
		webhookPort                   int
		webhookCertDir                string
		pprofBindAddress              string
		leaderElectionNamespace       string
		enableSveltosCtrl             bool
		enableSveltosExpireCtrl       bool
		defaultHelmTimeout            time.Duration
		maxConcurrentReconciles       int
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "", "The namespace to use for leader election.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&templatesRepoURL, "templates-repo-url", "oci://ghcr.io/k0rdent/kcm/charts",
		"The default repo URL to download provider and cluster templates (charts) from, prefix with oci:// for OCI registries.")
	flag.StringVar(&globalRegistry, "global-registry", "",
		"Global registry which will be passed as global.registry value for all providers and ClusterDeployments")
	flag.StringVar(&globalK0sURL, "global-k0s-url", "",
		"K0s URL prefix which will be passed directly as global.k0sURL to all ClusterDeployments configs")
	flag.StringVar(&registryCredentialsSecretName, "registry-creds-secret", "",
		"Name of a Secret containing authentication credentials for the registry.")
	flag.StringVar(&registryCertSecretName, "registry-cert-secret-name", "",
		"Name of a Secret containing root CA certificate (`ca.crt`) for connecting to the registry endpoint.")
	flag.StringVar(&k0sURLCertSecretName, "k0s-url-cert-secret-name", "", "Name of a Secret containing root CA certificate (`ca.crt`) for the k0s download URL.")
	flag.BoolVar(&insecureRegistry, "insecure-registry", false, "Allow connecting to an HTTP registry.")
	flag.BoolVar(&createManagement, "create-management", true, "Create a Management object with default configuration upon initial installation.")
	flag.BoolVar(&createAccessManagement, "create-access-management", true,
		"Create an AccessManagement object upon initial installation.")
	flag.BoolVar(&createRelease, "create-release", true, "Create an KCM Release upon initial installation.")
	flag.BoolVar(&createTemplates, "create-templates", true, "Create KCM Templates based on Release objects.")
	flag.BoolVar(&validateClusterUpgradePath, "validate-cluster-upgrade-path", true, "Specifies whether the ClusterDeployment upgrade path should be validated.")
	flag.StringVar(&kcmTemplatesChartName, "kcm-templates-chart-name", "kcm-templates",
		"The name of the helm chart with KCM Templates.")
	flag.BoolVar(&enableWebhook, "enable-webhook", true, "Enable admission webhook.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Admission webhook port.")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs/",
		"Webhook cert dir, only used when webhook-port is specified.")
	flag.StringVar(&pprofBindAddress, "pprof-bind-address", "", "The TCP address that the controller should bind to for serving pprof, \"0\" or empty value disables pprof")
	flag.BoolVar(&enableSveltosCtrl, "enable-sveltos-ctrl", true, "Enable Sveltos built-in provider controller")
	flag.BoolVar(&enableSveltosExpireCtrl, "enable-sveltos-expire-ctrl", false, "Enable SveltosCluster stuck (expired) tokens controller")
	flag.DurationVar(&defaultHelmTimeout, "default-helm-timeout", 0, "Specifies the timeout duration for Helm install or upgrade operations. If unset, Flux’s default value will be used")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 10, "Specifies the maximum number of concurrent reconciles that will be run for each controller.")

	// TODO: remove in one of the upcoming releases
	_ = flag.Bool("enable-telemetry", false, "[Deprecated] Has no effect, use a dedicated telemetry chart")
	(&telemetry.Config{}).BindFlags(flag.CommandLine)

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	flag.Usage = func() {
		var defaultUsage strings.Builder
		{
			oldOutput := flag.CommandLine.Output()
			flag.CommandLine.SetOutput(&defaultUsage)
			flag.PrintDefaults()
			flag.CommandLine.SetOutput(oldOutput)
		}

		_, _ = fmt.Fprintf(os.Stderr, "Usage of %s:\n", build.Name)
		_, _ = fmt.Fprint(os.Stderr, defaultUsage.String())
		_, _ = fmt.Fprintf(os.Stderr, "\nVersion: %s\nCommit: %s\nBuild time: %s\n", build.Version, build.Commit, build.Time)
	}
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	determinedRepositoryType, err := helmutil.DetermineDefaultRepositoryType(templatesRepoURL)
	if err != nil {
		setupLog.Error(err, "failed to determine default repository type")
		os.Exit(1)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	managerOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          true,
		LeaderElectionID:        "31c555b4.k0rdent.mirantis.com",
		LeaderElectionNamespace: leaderElectionNamespace,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,

		PprofBindAddress: pprofBindAddress,

		Cache: cache.Options{
			DefaultTransform: cache.TransformStripManagedFields(),
		},
	}

	if enableWebhook {
		managerOpts.WebhookServer = webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			TLSOpts: tlsOpts,
			CertDir: webhookCertDir,
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	if err = kcmv1.SetupIndexers(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to setup indexers")
		os.Exit(1)
	}

	record.InitFromRecorder(mgr.GetEventRecorderFor("kcm-controller-manager"))

	currentNamespace := kubeutil.CurrentNamespace()

	cfg := config{
		createManagement:              createManagement,
		templatesRepoURL:              templatesRepoURL,
		determinedRepositoryType:      determinedRepositoryType,
		registryCredentialsSecretName: registryCredentialsSecretName,
		registryCertSecretName:        registryCertSecretName,
		insecureRegistry:              insecureRegistry,
		createAccessManagement:        createAccessManagement,
		enableWebhook:                 enableWebhook,
		globalRegistry:                globalRegistry,
		globalK0sURL:                  globalK0sURL,
		k0sURLCertSecretName:          k0sURLCertSecretName,
		createRelease:                 createRelease,
		createTemplates:               createTemplates,
		kcmTemplatesChartName:         kcmTemplatesChartName,
		enableSveltosCtrl:             enableSveltosCtrl,
		enableSveltosExpireCtrl:       enableSveltosExpireCtrl,
		defaultHelmTimeout:            defaultHelmTimeout,
	}
	if err := setupControllers(mgr, currentNamespace, cfg); err != nil {
		setupLog.Error(err, "failed to setup controllers")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if enableWebhook {
		if err := setupWebhooks(mgr, currentNamespace, validateClusterUpgradePath); err != nil {
			setupLog.Error(err, "failed to setup webhooks")
			os.Exit(1)
		}
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupControllers(mgr ctrl.Manager, currentNamespace string, cfg config) error {
	var err error
	templateReconciler := controller.TemplateReconciler{
		Client:           mgr.GetClient(),
		CreateManagement: cfg.createManagement,
		SystemNamespace:  currentNamespace,
		DefaultRegistryConfig: helm.DefaultRegistryConfig{
			URL:                   cfg.templatesRepoURL,
			RepoType:              cfg.determinedRepositoryType,
			CredentialsSecretName: cfg.registryCredentialsSecretName,
			CertSecretName:        cfg.registryCertSecretName,
			Insecure:              cfg.insecureRegistry,
		},
	}

	if err = (&statemanagementprovider.Reconciler{
		Client:          mgr.GetClient(),
		SystemNamespace: currentNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "StateManagementProvider")
		return err
	}
	if err = (&controller.ClusterTemplateReconciler{
		TemplateReconciler: templateReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterTemplate")
		return err
	}
	if err = (&controller.ServiceTemplateReconciler{
		TemplateReconciler: templateReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServiceTemplate")
		return err
	}
	if err = (&controller.ProviderTemplateReconciler{
		TemplateReconciler: templateReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ProviderTemplate")
		return err
	}
	if err = (&controller.ManagementReconciler{
		SystemNamespace:               currentNamespace,
		CreateAccessManagement:        cfg.createAccessManagement,
		IsDisabledValidationWH:        !cfg.enableWebhook,
		GlobalRegistry:                cfg.globalRegistry,
		GlobalK0sURL:                  cfg.globalK0sURL,
		K0sURLCertSecretName:          cfg.k0sURLCertSecretName,
		RegistryCertSecretName:        cfg.registryCertSecretName,
		RegistryCredentialsSecretName: cfg.registryCredentialsSecretName,
		DefaultHelmTimeout:            cfg.defaultHelmTimeout,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Management")
		return err
	}
	if err = (&region.Reconciler{
		MgmtClient:                    mgr.GetClient(),
		IsDisabledValidationWH:        !cfg.enableWebhook,
		SystemNamespace:               currentNamespace,
		GlobalRegistry:                cfg.globalRegistry,
		RegistryCertSecretName:        cfg.registryCertSecretName,
		RegistryCredentialsSecretName: cfg.registryCredentialsSecretName,
		DefaultHelmTimeout:            cfg.defaultHelmTimeout,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Region")
		return err
	}
	if err = (&controller.AccessManagementReconciler{
		Client:          mgr.GetClient(),
		SystemNamespace: currentNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AccessManagement")
		return err
	}

	templateChainReconciler := controller.TemplateChainReconciler{
		Client:          mgr.GetClient(),
		SystemNamespace: currentNamespace,
	}
	if err = (&controller.ClusterTemplateChainReconciler{
		TemplateChainReconciler: templateChainReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterTemplateChain")
		return err
	}
	if err = (&controller.ServiceTemplateChainReconciler{
		TemplateChainReconciler: templateChainReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServiceTemplateChain")
		return err
	}

	if err = (&controller.ReleaseReconciler{
		Client:                mgr.GetClient(),
		Config:                mgr.GetConfig(),
		CreateManagement:      cfg.createManagement,
		CreateRelease:         cfg.createRelease,
		CreateTemplates:       cfg.createTemplates,
		KCMTemplatesChartName: cfg.kcmTemplatesChartName,
		SystemNamespace:       currentNamespace,
		DefaultRegistryConfig: helm.DefaultRegistryConfig{
			URL:                   cfg.templatesRepoURL,
			RepoType:              cfg.determinedRepositoryType,
			CredentialsSecretName: cfg.registryCredentialsSecretName,
			CertSecretName:        cfg.registryCertSecretName,
			Insecure:              cfg.insecureRegistry,
		},
		DefaultHelmTimeout: cfg.defaultHelmTimeout,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Release")
		return err
	}

	if err = (&controller.CredentialReconciler{
		SystemNamespace: currentNamespace,
		MgmtClient:      mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Credential")
		return err
	}

	if err = (&controller.ManagementBackupReconciler{
		Client:          mgr.GetClient(),
		SystemNamespace: currentNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagementBackup")
		return err
	}

	if err = (&ipam.ClusterIPAMClaimReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterIPAMClaim")
		return err
	}
	if err = (&ipam.ClusterIPAMReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterIPAM")
		return err
	}

	if cfg.enableSveltosCtrl {
		// we'll add sveltos types to the scheme only in case sveltos integration is enabled
		setupLog.Info("adding sveltos types to the scheme")
		utilruntime.Must(addoncontrollerv1beta1.AddToScheme(scheme))
		utilruntime.Must(libsveltosv1beta1.AddToScheme(scheme))

		deploymentName := os.Getenv("KCM_NAME")

		setupLog.Info("setting up built-in ServiceSet controller")
		if err = (&sveltos.ServiceSetReconciler{
			SystemNamespace:         currentNamespace,
			AdapterName:             deploymentName,
			AdapterNamespace:        currentNamespace,
			MaxConcurrentReconciles: cfg.maxConcurrentReconciles,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ServiceSet")
			return err
		}
		setupLog.Info("setup for ServiceSet controller successful")
	}

	if cfg.enableSveltosExpireCtrl {
		if err = (&sveltos.ClusterReconciler{
			Client: mgr.GetClient(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "SveltosCluster")
			return err
		}
	}
	return nil
}

func setupWebhooks(mgr ctrl.Manager, currentNamespace string, validateClusterUpgradePath bool) error {
	if err := (&kcmwebhook.ClusterDeploymentValidator{
		ValidateClusterUpgradePath: validateClusterUpgradePath,
		SystemNamespace:            currentNamespace,
	}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ClusterDeployment")
		return err
	}
	if err := (&kcmwebhook.ClusterAuthenticationValidator{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ClusterAuthentication")
		return err
	}
	if err := (&kcmwebhook.ManagementValidator{SystemNamespace: currentNamespace}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "Management")
		return err
	}
	if err := (&kcmwebhook.RegionValidator{SystemNamespace: currentNamespace}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "Region")
		return err
	}
	if err := (&kcmwebhook.AccessManagementValidator{SystemNamespace: currentNamespace}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "AccessManagement")
		return err
	}
	if err := (&kcmwebhook.ClusterTemplateChainValidator{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ClusterTemplateChain")
		return err
	}
	if err := (&kcmwebhook.ServiceTemplateChainValidator{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ServiceTemplateChain")
		return err
	}

	templateValidator := kcmwebhook.TemplateValidator{
		SystemNamespace: currentNamespace,
	}
	if err := (&kcmwebhook.ClusterTemplateValidator{TemplateValidator: templateValidator}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ClusterTemplate")
		return err
	}
	if err := (&kcmwebhook.ServiceTemplateValidator{TemplateValidator: templateValidator}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ServiceTemplate")
		return err
	}
	if err := (&kcmwebhook.ProviderTemplateValidator{TemplateValidator: templateValidator}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ProviderTemplate")
		return err
	}
	if err := (&kcmwebhook.ReleaseValidator{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "Release")
		return err
	}
	return nil
}
