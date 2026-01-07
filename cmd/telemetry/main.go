// Copyright 2025
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
	"crypto/md5"
	"flag"
	"fmt"
	"os"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/K0rdent/kcm/internal/telemetry"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

var (
	scheme   = schemeutil.MustGetManagementScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	for _, f := range []func(*runtime.Scheme) error{
		addoncontrollerv1beta1.AddToScheme,
		metav1.AddMetaToScheme,
	} {
		if err := f(scheme); err != nil {
			panic(err)
		}
	}

	var (
		metricsAddr      string
		probeAddr        string
		pprofBindAddress string
		secureMetrics    bool
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false, "If set the metrics endpoint is served securely")
	flag.StringVar(&pprofBindAddress, "pprof-bind-address", "", "The TCP address that the controller should bind to for serving pprof, \"0\" or empty value disables pprof. Works only if --metrics-secure=true")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)

	telemetryCfg := &telemetry.Config{}
	telemetryCfg.BindFlags(flag.CommandLine)

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if !secureMetrics {
		pprofBindAddress = "0"
	}

	managerOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         true,
		LeaderElectionID:       fmt.Sprintf("%x.k0rdent.mirantis.com", md5.Sum([]byte(telemetryCfg.Mode))),
		PprofBindAddress:       pprofBindAddress,
		Cache: cache.Options{
			DefaultTransform: cache.TransformStripManagedFields(),
		},
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	telemetryCfg.ParentClient = mgr.GetClient()
	telemetryCfg.DirectReader = mgr.GetAPIReader()
	tr, err := telemetry.NewRunner(telemetryCfg)
	if err != nil {
		setupLog.Error(err, "failed to construct telemetry runner")
		os.Exit(1)
	}

	if tr.Enabled() {
		if err := mgr.Add(tr); err != nil {
			setupLog.Error(err, "unable to add telemetry runner")
			os.Exit(1)
		}
	} else {
		setupLog.Info("Telemetry collection is effectively disabled, will keep the instance without exiting")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
