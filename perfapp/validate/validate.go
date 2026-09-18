package validate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/codeready-toolchain/toolchain-common/pkg/usersignup"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"
	"github.com/codeready-toolchain/toolchain-e2e/setup/templates"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var usernameForbiddenPrefixes = []string{"openshift", "kube", "default", "redhat", "sandbox"}
var usernameForbiddenSuffixes = []string{"admin"}

type Input struct {
	Kubeconfig   []byte
	Template     []byte
	TemplateName string
	SetupRuns    []run.SetupRun
	Workloads    []string
	Testname     string
}

type Result struct {
	RESTConfig   *rest.Config
	Client       client.Client
	APIServer    string
	SetupRuns    []run.SetupRun
	Template     []byte
	TemplateName string
	Workloads    []string
	Testname     string
}

type ClientFactory func(kubeconfig []byte) (client.Client, *rest.Config, error)

func RESTConfig(kubeconfig []byte) (*rest.Config, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("invalid kubeconfig: %w", err)
	}
	if cfg.Host == "" {
		return nil, errors.New("invalid kubeconfig: missing API server")
	}
	return cfg, nil
}

func Submit(ctx context.Context, in Input, newClient ClientFactory) (*Result, error) {
	if newClient == nil {
		return nil, errors.New("missing kubernetes client factory")
	}
	cfg, err := RESTConfig(in.Kubeconfig)
	if err != nil {
		return nil, err
	}
	runs, err := NormalizeSetupRuns(in.SetupRuns)
	if err != nil {
		return nil, err
	}
	if err := Workloads(in.Workloads); err != nil {
		return nil, err
	}
	templateName, err := Template(runs, in.Template, in.TemplateName)
	if err != nil {
		return nil, err
	}

	cl, restCfg, err := newClient(in.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create client from kubeconfig: %w", err)
	}
	if restCfg == nil {
		restCfg = cfg
	}
	if err := ClusterAdmin(ctx, cl); err != nil {
		return nil, err
	}

	return &Result{
		RESTConfig:   restCfg,
		Client:       cl,
		APIServer:    run.NormalizeAPIServer(restCfg.Host),
		SetupRuns:    runs,
		Template:     in.Template,
		TemplateName: templateName,
		Workloads:    in.Workloads,
		Testname:     in.Testname,
	}, nil
}

func ClusterAdmin(ctx context.Context, cl client.Client) error {
	sar := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Verb:     "*",
				Group:    "*",
				Resource: "*",
			},
		},
	}
	if err := cl.Create(ctx, sar); err != nil {
		return fmt.Errorf("cluster-admin check failed: %w", err)
	}
	if !sar.Status.Allowed {
		return errors.New("kubeconfig does not have cluster-admin equivalent access")
	}
	return nil
}

func NormalizeSetupRuns(runs []run.SetupRun) ([]run.SetupRun, error) {
	if len(runs) == 0 {
		return nil, errors.New("at least one setup run is required")
	}
	seen := map[string]int{}
	out := make([]run.SetupRun, len(runs))
	for i, sr := range runs {
		if sr.Users < 1 {
			return nil, fmt.Errorf("setup run %d: users must be at least 1", i)
		}
		if sr.Custom < 0 || sr.Custom > sr.Users {
			return nil, fmt.Errorf("setup run %d: custom must be between 0 and users (%d)", i, sr.Users)
		}
		if sr.Default < 0 || sr.Default > sr.Users {
			return nil, fmt.Errorf("setup run %d: default must be between 0 and users (%d)", i, sr.Users)
		}
		if strings.TrimSpace(sr.Username) == "" {
			return nil, fmt.Errorf("setup run %d: username is required", i)
		}
		if prev, ok := seen[sr.Username]; ok {
			return nil, fmt.Errorf("setup run %d: username %q is already used by setup run %d", i, sr.Username, prev)
		}
		seen[sr.Username] = i
		transformed := usersignup.TransformUsername(sr.Username, usernameForbiddenPrefixes, usernameForbiddenSuffixes)
		if transformed != sr.Username {
			return nil, fmt.Errorf("setup run %d: username %q would be transformed to %q by Dev Sandbox username restrictions", i, sr.Username, transformed)
		}
		out[i] = sr
	}
	return out, nil
}

func Template(runs []run.SetupRun, content []byte, name string) (string, error) {
	needed := false
	for _, sr := range runs {
		if sr.Custom > 0 {
			needed = true
			break
		}
	}
	if !needed {
		return "", nil
	}
	if len(content) == 0 {
		return "", errors.New("an OpenShift Template is required when any setup run has custom users greater than 0")
	}
	if _, err := templates.GetTemplateFromContent(content); err != nil {
		return "", fmt.Errorf("invalid template: %w", err)
	}
	if name == "" {
		name = "template.yaml"
	}
	return name, nil
}

func Workloads(values []string) error {
	for _, w := range values {
		pair := strings.Split(w, ":")
		if len(pair) != 2 || pair[0] == "" || pair[1] == "" {
			return fmt.Errorf("invalid workload %q: values must be namespace:name pairs", w)
		}
	}
	return nil
}
