// cronops-server: REST API for the web UI plus static file hosting.
// Stateless facade over the Kubernetes API.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
	"github.com/AlexPokatilov/CronOps/internal/apiserver"
	"github.com/AlexPokatilov/CronOps/internal/auth"
)

func main() {
	var addr string
	var staticDir string
	var dev bool
	flag.StringVar(&addr, "listen", ":8090", "Address the HTTP server binds to.")
	flag.StringVar(&staticDir, "static", "./web/dist", "Directory with the built web UI (empty to disable).")
	flag.BoolVar(&dev, "dev", false,
		"Development mode: CORS for the vite dev server and admin/admin login (never use in production).")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)
	log := logger.WithName("cronops-server")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(cronopsv1alpha1.AddToScheme(scheme))

	k8sClient, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "unable to create kubernetes client")
		os.Exit(1)
	}

	namespace := os.Getenv("CRONOPS_NAMESPACE")
	if namespace == "" {
		namespace = "cronops"
	}

	authCfg := auth.Config{
		Client:       k8sClient,
		Namespace:    namespace,
		SecureCookie: !dev,
	}
	devCORS := ""
	if dev {
		log.Info("DEV MODE: using admin/admin credentials and permissive CORS — do not use in production")
		authCfg.DevUsers = map[string]string{"admin": "admin"}
		devCORS = "http://localhost:5173"
	}
	authSvc, err := auth.New(authCfg)
	if err != nil {
		log.Error(err, "unable to initialise auth service")
		os.Exit(1)
	}

	if staticDir != "" {
		if _, err := os.Stat(staticDir); err != nil {
			log.Info("static dir not found, serving API only", "dir", staticDir)
			staticDir = ""
		}
	}

	srv := &apiserver.Server{
		Client:        k8sClient,
		Auth:          authSvc,
		StaticDir:     staticDir,
		DevCORSOrigin: devCORS,
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("listening", "addr", addr, "namespace", namespace, "static", staticDir)
	if err := httpServer.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
