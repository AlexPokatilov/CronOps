// cronops-controller: watches HttpCronJob resources, schedules and executes
// HTTP calls, and writes results back into resource status.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
	"github.com/AlexPokatilov/CronOps/internal/controller"
	"github.com/AlexPokatilov/CronOps/internal/executor"
	"github.com/AlexPokatilov/CronOps/internal/scheduler"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cronopsv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election so only one replica schedules and executes runs.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cronops-controller.cronops.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	sched := scheduler.New()
	if err := mgr.Add(sched); err != nil {
		setupLog.Error(err, "unable to add scheduler runnable")
		os.Exit(1)
	}

	//nolint:staticcheck // legacy events API kept until controller-runtime removes it
	recorder := mgr.GetEventRecorderFor("cronops-controller")
	tracker := controller.NewRunTracker()

	reconciler := &controller.HttpCronJobReconciler{
		Client:    mgr.GetClient(),
		Recorder:  recorder,
		Scheduler: sched,
		Tracker:   tracker,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HttpCronJob")
		os.Exit(1)
	}

	runReconciler := &controller.HttpCronJobRunReconciler{
		Client:   mgr.GetClient(),
		Recorder: recorder,
		Executor: executor.New(mgr.GetClient()),
		Tracker:  tracker,
	}
	if err := runReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HttpCronJobRun")
		os.Exit(1)
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
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
