package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/validate"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "address to listen on (app is localhost-only behind oauth2-proxy)")
	namespace := flag.String("namespace", "", "app namespace for Test Run ConfigMaps (default: in-cluster namespace)")
	gitURI := flag.String("git-uri", run.GitURI, "Git repository cloned by the perf-job BuildConfig")
	gitRef := flag.String("git-ref", run.GitRef, "Git branch, tag, or commit cloned by the perf-job BuildConfig")
	flag.Parse()

	ns := *namespace
	if ns == "" {
		var err error
		ns, err = inClusterNamespace()
		if err != nil {
			log.Fatalf("--namespace is required when not running in-cluster: %v", err)
		}
	}

	restCfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("kubeconfig: %v", err)
	}
	scheme, err := Scheme()
	if err != nil {
		log.Fatalf("scheme: %v", err)
	}
	appCl, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("app client: %v", err)
	}

	srv, err := NewServer(appCl, ns, func(kube []byte) (client.Client, *rest.Config, error) {
		cfg, err := validate.RESTConfig(kube)
		if err != nil {
			return nil, nil, err
		}
		cl, err := client.New(cfg, client.Options{Scheme: scheme})
		return cl, cfg, err
	})
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	srv.Build = BuildConfiguration{GitURI: *gitURI, GitRef: *gitRef}
	if err := srv.Build.Validate(); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Poller().Run(ctx, 15*time.Second)

	log.Printf("perfapp listening on %s (namespace %s, git %s@%s)", *listen, ns, srv.Build.URI(), srv.Build.Ref())
	if err := http.ListenAndServe(*listen, srv.Handler()); err != nil { //nolint:gosec
		log.Fatal(err)
	}
}

func inClusterNamespace() (string, error) {
	const path = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	ns := strings.TrimSpace(string(b))
	if ns == "" {
		loading := clientcmd.NewDefaultClientConfigLoadingRules()
		ns, _, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, &clientcmd.ConfigOverrides{}).Namespace()
		return ns, err
	}
	return ns, nil
}
