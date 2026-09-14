package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	previewv1 "github.com/Garvity/preview-operator/api/v1alpha1"
	"github.com/Garvity/preview-operator/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var version = "dev"

func main() {
	var namespace, metricsAddr, probeAddr string
	var leaderElection, showVersion bool
	flag.StringVar(&namespace, "watch-namespace", os.Getenv("POD_NAMESPACE"), "Required namespace to watch and use for leader election")
	flag.StringVar(&metricsAddr, "metrics-bind-address", "127.0.0.1:8080", "Metrics listener; use 0 to disable")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health/readiness listener")
	flag.BoolVar(&leaderElection, "leader-elect", true, "Enable leader election")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	if showVersion {
		fmt.Println(version)
		return
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := ctrl.Log.WithName("setup")
	if namespace == "" {
		logger.Error(fmt.Errorf("watch namespace is required"), "Set --watch-namespace or POD_NAMESPACE")
		os.Exit(1)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, previewv1.AddToScheme} {
		if err := add(scheme); err != nil {
			logger.Error(err, "Register API scheme")
			os.Exit(1)
		}
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme, Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}},
		Metrics: metricsserver.Options{BindAddress: metricsAddr}, HealthProbeBindAddress: probeAddr,
		LeaderElection: leaderElection, LeaderElectionID: "preview-operator.preview.garv.dev", LeaderElectionNamespace: namespace,
		LeaderElectionReleaseOnCancel: true, GracefulShutdownTimeout: duration(30 * time.Second),
	})
	if err != nil {
		logger.Error(err, "Create manager")
		os.Exit(1)
	}
	r := &controller.PreviewEnvironmentReconciler{Client: mgr.GetClient(), Scheme: scheme, Recorder: mgr.GetEventRecorderFor("preview-operator")}
	if err := r.SetupWithManager(mgr); err != nil {
		logger.Error(err, "Register controller")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "Register health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("cache", func(req *http.Request) error {
		ctx, cancel := context.WithTimeout(req.Context(), time.Second)
		defer cancel()
		if !mgr.GetCache().WaitForCacheSync(ctx) {
			return fmt.Errorf("cache is not synchronized")
		}
		return nil
	}); err != nil {
		logger.Error(err, "Register readiness check")
		os.Exit(1)
	}
	logger.Info("Starting preview operator", "version", version, "namespace", namespace)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "Run manager")
		os.Exit(1)
	}
}

func duration(v time.Duration) *time.Duration { return &v }
